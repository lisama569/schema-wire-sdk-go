// schemagen 根据 IDL schema 文件生成 Go 编解码代码。
//
// 用法:
//
//	schemagen -schema path/to/schema.idl -out path/to/dir [-pkg name]
//
// schema 校验未通过时不产出任何文件，退出码非零。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lisama569/schema-wire-sdk-go/gen"
	"github.com/lisama569/schema-wire-sdk-go/schema"
)

func main() {
	schemaPath := flag.String("schema", "", "IDL schema 文件路径")
	outDir := flag.String("out", "", "生成产物输出目录")
	pkg := flag.String("pkg", "", "覆盖生成文件的包名（默认使用 schema 中的 package）")
	flag.Parse()

	if *schemaPath == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "用法: schemagen -schema <file.idl> -out <dir> [-pkg name]")
		os.Exit(2)
	}

	src, err := os.ReadFile(*schemaPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取 schema 失败:", err)
		os.Exit(1)
	}
	f, err := schema.Parse(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	files, err := gen.Generate(f, gen.Options{PackageName: *pkg})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := gen.WriteFiles(*outDir, files); err != nil {
		fmt.Fprintln(os.Stderr, "写入产物失败:", err)
		os.Exit(1)
	}
	for name := range files {
		fmt.Println("已生成:", name)
	}
}
