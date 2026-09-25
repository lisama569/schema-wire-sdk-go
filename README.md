# schema-wire-sdk-go

由 IDL schema 驱动的版本化二进制编解码 SDK：根据 schema 生成 Go 类型定义与
编解码代码，并保证生成产物与运行时在协议持续演进时仍然互通。

## 模块结构

| 包 | 职责 |
|---|---|
| `schema` | IDL 解析（`Parse`）与语义校验（`Validate`），错误按类别可区分 |
| `gen` | 代码生成（`Generate`）与原子化写盘（`WriteFiles`） |
| `wire` | 版本化二进制编解码运行时，未知字段保留，错误类别可区分 |
| `cmd/schemagen` | 命令行生成工具 |
| `testfix` | 两套版本化生成夹具（`oldshop`/`newshop`）与跨版本集成测试 |

## Schema 语法

```idl
package shop;

// 消息声明必须带版本区间 [min, max]。
message User version 1..3 {
    // <编号>: <类型> <名称> [since N] [until M];
    1: string name since 1;
    2: int32 age since 1 until 2;   // 版本 3 起移除
    3: string email since 2;        // 版本 2 引入
    4: bool active;                 // 缺省 since = 消息最小版本
}

message Order version 1..2 {
    1: int64 id;
    2: User buyer;                  // 嵌套消息
    3: bytes note since 2;
}

// 兼容性声明：同一消息两个版本间是否兼容。
compat User 1 2;
incompat User 1 3;
```

- 内建类型：`int32`、`int64`、`bool`、`string`、`bytes`；其余标识符必须是
  已声明的消息名（支持自引用与嵌套）。
- `since`/`until` 均为闭区间；`until` 缺省表示字段在消息最大版本仍有效。
- 注释：`//` 至行尾。

### 校验规则与失败类别

`schema.Validate` 返回 `*schema.ValidationError`，`Kind` 字段区分失败原因：

| Kind | 触发条件 |
|---|---|
| `ErrKindDuplicateField` | 同一消息内字段编号重复；消息重复声明 |
| `ErrKindUnknownType` | 字段或兼容性声明引用未声明的类型 |
| `ErrKindBadVersionRange` | 版本区间下界大于上界、字段 `since/until` 越界或倒置、兼容性声明版本越界或两端相同 |
| `ErrKindCompatConflict` | 同一对版本同时被声明为兼容与不兼容 |

语法错误返回 `*schema.ParseError`（含行列号）。**校验未通过时
`gen.Generate` 不产出任何生成结果。**

## 线上格式与版本兼容规则

消息编码：

```
varint(version)                      -- 消息版本头
重复字段: varint(fieldNum<<1|wireType) + 载荷
  wireType=0 (varint): zigzag 整数 / 布尔
  wireType=1 (bytes):  varint(len) + 原始字节（string/bytes/嵌套消息）
```

兼容规则：

1. **版本门禁**：编码/解码均校验版本头落在生成代码的 `[MinVersion, MaxVersion]`
   区间内，越界返回 `wire.ErrIncompatibleVersion`。
2. **字段门禁**：编码时仅写入在目标版本有效的字段（`since <= v <= until`）。
3. **未知字段保留**：解码时遇到的未知字段原样存入 `UnknownFields`，重新编码时
   按原顺序逐字节写回——旧运行时中转新数据不丢字段。
4. **损坏数据拒绝**：截断、长度越界、非法标签、varint 溢出等返回
   `wire.ErrCorrupt`。
5. 两类错误可用 `errors.Is` 区分：

```go
err := msg.Decode(data)
switch {
case errors.Is(err, wire.ErrIncompatibleVersion): // 版本不兼容
case errors.Is(err, wire.ErrCorrupt):             // 数据损坏
}
```

注意：本格式是自描述的，落在字段边界上的截断前缀本身是一条合法的更短消息；
只有截断落在字段编码内部才会被判定为损坏。

## 生成流程

```bash
# 命令行生成（校验失败时不产出任何文件，退出码非零）
go run ./cmd/schemagen -schema testfix/newshop.idl -out testfix/newshop
```

或在代码中：

```go
f, err := schema.Parse(idl)          // 解析
files, err := gen.Generate(f, gen.Options{}) // 内含 Validate；失败无产出
err = gen.WriteFiles(dir, files)     // 先写临时文件再统一 rename，失败不留半成品
```

生成保证：

- **确定性**：同一 schema 重复生成的结果逐字节一致（字段按编号升序、文件按名
  排序、导出标识由字段名经固定规则导出）。
- **并发安全**：`Generate` 不持有共享状态，可并发生成多个目标包且互不污染；
  不同输出目录的 `WriteFiles` 可并发。
- **原子性**：`WriteFiles` 先将全部内容写入临时文件并落盘，再统一 rename；
  任一环节失败都会清理临时文件并尽力回滚，不留下半成品。

## 本地验证

```bash
go build ./...        # 编译
go vet ./...          # 静态检查
go test ./...         # 全部测试
go test ./... -race   # 含竞态检测（覆盖并发生成用例）
go test ./testfix/ -v # 跨版本集成测试，日志含输入、判定依据与错误归类
```

测试覆盖：跨版本往返、未知字段保留（逐字节一致）、截断/越界数据、版本不兼容、
schema 各类校验失败、生成确定性、并发生成无污染、写盘失败无半成品。

重新生成测试夹具：

```bash
go run ./cmd/schemagen -schema testfix/oldshop.idl -out testfix/oldshop
go run ./cmd/schemagen -schema testfix/newshop.idl -out testfix/newshop
```
