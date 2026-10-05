// schemagen 根据 IDL schema 文件生成 Go 编解码代码。
//
// 用法:
//
//	schemagen -schema path/to/new.idl -out path/to/dir [-baseline path/to/published.idl] [-pkg name]
//
// 发布前在 CI 中带上 -baseline 指向线上已发布的 schema：演进审查不通过时
// 不产出任何文件并以退出码 10 退出；schema 非法退出 11；生成包无法编译
// 退出 12；语法错误退出 13。CI 可据此分别判定“被演进审查拦下”等情形。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/lisama569/schema-wire-sdk-go/gen"
	"github.com/lisama569/schema-wire-sdk-go/review"
	"github.com/lisama569/schema-wire-sdk-go/schema"
)

// 退出码：不同失败类别可区分，便于 CI 分流处理。
const (
	exitOK             = 0
	exitUsage          = 2
	exitIO             = 1
	exitReviewRejected = 10 // 演进审查判定改动不安全
	exitSchemaInvalid  = 11 // schema 语义校验失败
	exitUncompilable   = 12 // schema 合法但生成包编译不过
	exitSyntax         = 13 // IDL 语法错误
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("schemagen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	schemaPath := fs.String("schema", "", "待发布的 IDL schema 文件路径")
	baselinePath := fs.String("baseline", "", "线上已发布的 IDL schema 文件路径（提供时启用演进审查）")
	outDir := fs.String("out", "", "生成产物输出目录")
	pkg := fs.String("pkg", "", "覆盖生成文件的包名（默认使用 schema 中的 package）")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	if *schemaPath == "" || *outDir == "" {
		fmt.Fprintln(stderr, "用法: schemagen -schema <new.idl> -out <dir> [-baseline <published.idl>] [-pkg name]")
		return exitUsage
	}

	f, code := readAndParse(*schemaPath, stderr)
	if code != exitOK {
		return code
	}

	var baseline *schema.File
	if *baselinePath != "" {
		b, code := readAndParse(*baselinePath, stderr)
		if code != exitOK {
			fmt.Fprintln(stderr, "读取基线 schema 失败")
			return code
		}
		baseline = b
	}

	files, err := gen.GenerateRelease(baseline, f, gen.Options{PackageName: *pkg})
	if err != nil {
		switch {
		case errors.Is(err, review.ErrReviewRejected):
			fmt.Fprintln(stderr, err)
			fmt.Fprintln(stderr, "演进审查未通过：已拦截本次发布，未产出任何生成文件。")
			return exitReviewRejected
		}
		var ve *schema.ValidationError
		if errors.As(err, &ve) {
			fmt.Fprintln(stderr, err)
			return exitSchemaInvalid
		}
		var ue *gen.UncompilableError
		if errors.As(err, &ue) {
			fmt.Fprintln(stderr, err)
			return exitUncompilable
		}
		var pe *schema.ParseError
		if errors.As(err, &pe) {
			fmt.Fprintln(stderr, err)
			return exitSyntax
		}
		fmt.Fprintln(stderr, "生成失败:", err)
		return exitIO
	}

	if err := gen.WriteFiles(*outDir, files); err != nil {
		fmt.Fprintln(stderr, "写入产物失败:", err)
		return exitIO
	}
	for name := range files {
		fmt.Fprintf(stdout, "已生成: %s\n", name)
	}
	return exitOK
}

// readAndParse 读取并解析一个 IDL 文件，按失败类别返回对应退出码。
func readAndParse(path string, stderr io.Writer) (*schema.File, int) {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(stderr, "读取 schema 失败:", err)
		return nil, exitIO
	}
	f, err := schema.Parse(string(src))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, exitSyntax
	}
	return f, exitOK
}
