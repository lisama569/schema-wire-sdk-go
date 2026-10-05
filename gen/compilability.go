package gen

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/format"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/lisama569/schema-wire-sdk-go/schema"
)

// sdkModulePath 是生成产物运行时依赖的 SDK 模块路径。
const sdkModulePath = "github.com/lisama569/schema-wire-sdk-go"

// UncompilableError 表示 schema 自身能通过解析与语义校验，但由它生成的
// Go 包无法通过编译。它与 *schema.ValidationError（语义非法）以及
// *review 报告（演进不安全）是不同的失败类别，CI 可分别判定。
type UncompilableError struct {
	// Output 是编译器输出（已截断），用于定位生成代码的问题。
	Output string
}

func (e *UncompilableError) Error() string {
	return fmt.Sprintf("生成产物无法通过编译（schema 合法但生成包编译失败）:\n%s", e.Output)
}

// Is 让 errors.Is(err, ErrUncompilable) 可用。
func (e *UncompilableError) Is(target error) bool {
	_, ok := target.(*UncompilableError)
	return ok
}

// ErrUncompilable 是 UncompilableError 的哨兵，便于 errors.As/Is 归类。
var ErrUncompilable = &UncompilableError{}

// reservedNames 是生成类型上由 SDK 占用的标识符，字段导出名不得与之冲突。
var reservedNames = map[string]bool{
	"Encode": true, "EncodeVersion": true,
	"Decode": true, "DecodeWith": true,
	"UnknownFields": true,
}

// staticCompilabilityCheck 在不依赖 Go 工具链的前提下拦截必然编译失败的情形：
// 包名非法、消息名是 Go 关键字、字段名是关键字、不同字段导出后同名、
// 字段导出名撞上 SDK 生成的方法/字段名。
func staticCompilabilityCheck(f *schema.File, pkg string) error {
	if !token.IsIdentifier(pkg) {
		return &UncompilableError{Output: fmt.Sprintf("生成包名 %q 不是合法 Go 标识符", pkg)}
	}
	for _, m := range f.Messages {
		if token.IsKeyword(m.Name) {
			return &UncompilableError{Output: fmt.Sprintf(
				"消息 %q 的名称是 Go 关键字，无法作为生成的类型名", m.Name)}
		}
		seen := map[string]string{} // 导出名 -> 原始字段名
		for _, fld := range m.Fields {
			if token.IsKeyword(fld.Name) {
				return &UncompilableError{Output: fmt.Sprintf(
					"消息 %q 的字段 %q 是 Go 关键字，无法作为结构体字段名", m.Name, fld.Name)}
			}
			exp := schema.ExportedName(fld.Name)
			if reservedNames[exp] {
				return &UncompilableError{Output: fmt.Sprintf(
					"消息 %q 的字段 %q 导出名 %q 与 SDK 生成的方法/保留字段冲突",
					m.Name, fld.Name, exp)}
			}
			if prev, dup := seen[exp]; dup {
				return &UncompilableError{Output: fmt.Sprintf(
					"消息 %q 的字段 %q 与 %q 导出后同名 %q（snake_case 折叠冲突）",
					m.Name, prev, fld.Name, exp)}
			}
			seen[exp] = fld.Name
		}
	}
	return nil
}

// buildCache 按生成内容哈希缓存 go build 结论，避免同一产物重复编译。
var (
	buildCacheMu sync.Mutex
	buildCache   = map[string]error{}
)

// sdkRoot 返回 SDK 模块根目录（含 go.mod），用于 replace 到临时校验模块。
func sdkRoot() string {
	_, file, _, _ := runtime.Caller(0) // .../<sdkroot>/gen/compilability.go
	return filepath.Dir(filepath.Dir(file))
}

// ensureCompilable 对生成结果做可编译性预检：先做纯静态快检，
// 再在一个临时模块中对生成包执行真实的 go build。
// 任何编译失败都以 *UncompilableError 返回，绝不返回部分产物。
func ensureCompilable(f *schema.File, pkg string, files map[string][]byte) error {
	if err := staticCompilabilityCheck(f, pkg); err != nil {
		return err
	}

	// 内容哈希作为缓存键（文件按名排序后摘要，保证确定性）。
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(files[n])
		h.Write([]byte{0})
	}
	key := hex.EncodeToString(h.Sum(nil))

	buildCacheMu.Lock()
	err, hit := buildCache[key]
	buildCacheMu.Unlock()
	if hit {
		return err
	}
	err = runGoBuildCheck(files)
	buildCacheMu.Lock()
	buildCache[key] = err
	buildCacheMu.Unlock()
	return err
}

func runGoBuildCheck(files map[string][]byte) error {
	dir, err := os.MkdirTemp("", "schema-wire-gencheck-")
	if err != nil {
		return fmt.Errorf("创建可编译性预检临时目录失败: %w", err)
	}
	defer os.RemoveAll(dir)

	// 临时模块：replace 到当前 SDK 模块，解析生成代码里的 wire 依赖；
	// replace 指向本地路径，整个过程不需要联网。
	gomod := fmt.Sprintf("module gencheck\n\ngo 1.25.0\n\nrequire %s v0.0.0\n\nreplace %s => %s\n",
		sdkModulePath, sdkModulePath, sdkRoot())
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		return fmt.Errorf("写入预检 go.mod 失败: %w", err)
	}

	// 生成文件全部放进同一包目录。
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("创建预检包目录失败: %w", err)
	}
	for name, content := range files {
		if _, err := format.Source(content); err != nil { // 双保险
			return &UncompilableError{Output: fmt.Sprintf("生成代码无法 gofmt: %v", err)}
		}
		if err := os.WriteFile(filepath.Join(outDir, name), content, 0o644); err != nil {
			return fmt.Errorf("写入预检包文件失败: %w", err)
		}
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GO111MODULE=on", "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if _, lerr := exec.LookPath("go"); lerr != nil {
		return fmt.Errorf("可编译性预检需要 Go 工具链，但未找到 go 命令: %w（原始输出: %s）",
			lerr, strings.TrimSpace(string(output)))
	}
	out := strings.TrimSpace(string(output))
	if len(out) > 4000 {
		out = out[:4000] + "\n...(编译器输出已截断)"
	}
	return &UncompilableError{Output: out}
}
