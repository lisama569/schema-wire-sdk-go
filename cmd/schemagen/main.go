// Command schemagen 根据 IDL schema 生成版本化的 Go 二进制编解码代码。
//
// 用法:
//
//	schemagen -schema path/to/x.schema -out ./internal/pb -package pb
//
// 校验未通过或生成失败时进程以非零状态退出，并打印可区分的错误类别。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/lisama569/schema-wire-sdk-go/codegen"
	"github.com/lisama569/schema-wire-sdk-go/schema"
)

func main() {
	var (
		schemaPath = flag.String("schema", "", "path to the IDL schema file (required)")
		outDir     = flag.String("out", "", "output directory for generated Go files (required)")
		pkgName    = flag.String("package", "", "generated Go package name (defaults to schema package)")
	)
	flag.Parse()
	if *schemaPath == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "usage: schemagen -schema x.schema -out dir [-package name]")
		os.Exit(2)
	}
	if err := run(*schemaPath, *outDir, *pkgName); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func run(schemaPath, outDir, pkgName string) error {
	f, err := os.Open(schemaPath)
	if err != nil {
		return fmt.Errorf("open schema: %w", err)
	}
	defer f.Close()

	s, err := schema.Load(schemaPath, f)
	if err != nil {
		return err // *schema.LoadError 已携带可区分类别
	}
	res, err := codegen.Generate(context.Background(), s, outDir, codegen.Options{
		GoPackage: pkgName,
	})
	if err != nil {
		return err
	}
	fmt.Printf("schemagen: wrote %d file(s) to %s\n", len(res.Files), outDir)
	return nil
}
