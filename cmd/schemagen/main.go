// schemagen 根据 IDL schema 文件生成 Go 编解码代码。
//
// 用法:
//
//	schemagen -schema path/to/schema.idl -out path/to/dir [-pkg name] [-old path/to/published.idl]
//
// 退出码（供 CI 区分拦截原因）:
//
//	0  成功
//	1  一般失败（IO、语法、schema 校验、写盘等；错误信息中含可区分类别）
//	2  用法错误
//	3  协议演进审查未通过（提供了 -old 且新旧 schema 对比不安全），不产出任何文件
//
// schema 校验未通过或演进审查未通过时均不产出任何文件。
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/lisama569/schema-wire-sdk-go/gen"
	"github.com/lisama569/schema-wire-sdk-go/schema"
)

// 退出码约定。
const (
	exitOK = iota
	exitFailure
	exitUsage
	exitEvolutionRejected
)

func main() {
	os.Exit(run())
}

func run() int {
	schemaPath := flag.String("schema", "", "IDL schema 文件路径")
	outDir := flag.String("out", "", "生成产物输出目录")
	pkg := flag.String("pkg", "", "覆盖生成文件的包名（默认使用 schema 中的 package）")
	oldPath := flag.String("old", "", "线上已发布的 schema 文件路径（提供时先做协议演进审查）")
	flag.Parse()

	if *schemaPath == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "用法: schemagen -schema <file.idl> -out <dir> [-pkg name] [-old published.idl]")
		return exitUsage
	}

	f, err := loadSchema(*schemaPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailure
	}

	// 协议演进审查：不通过则不产出任何生成结果，以专属退出码结束。
	if *oldPath != "" {
		oldF, err := loadSchema(*oldPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "读取已发布 schema 失败:", err)
			return exitFailure
		}
		if err := schema.ReviewEvolution(oldF, f); err != nil {
			var ee *schema.EvolutionError
			if errors.As(err, &ee) {
				fmt.Fprintln(os.Stderr, err)
				return exitEvolutionRejected
			}
			fmt.Fprintln(os.Stderr, err)
			return exitFailure
		}
		fmt.Println("演进审查: 通过（新旧 schema 对比安全）")
	}

	files, err := gen.Generate(f, gen.Options{PackageName: *pkg})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailure
	}
	if err := gen.WriteFiles(*outDir, files); err != nil {
		fmt.Fprintln(os.Stderr, "写入产物失败:", err)
		return exitFailure
	}
	for name := range files {
		fmt.Println("已生成:", name)
	}
	return exitOK
}

func loadSchema(path string) (*schema.File, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 schema 失败: %w", err)
	}
	return schema.Parse(string(src))
}
