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
| `ErrKindCodegenUnsafe` | 语义合法但生成的 Go 代码无法编译（标识符是 Go 关键字、字段导出名互相冲突或占用生成代码的成员名等），在生成之前即被拒绝 |

语法错误返回 `*schema.ParseError`（含行列号）。**校验未通过时
`gen.Generate` 不产出任何生成结果。**

## 协议演进审查

改 IDL 之前，用 `schema.ReviewEvolution(oldF, newF)` 把线上已发布的 schema
与待发布的 schema 放一起对比，判定这次改动是否安全。不安全时返回
`*schema.EvolutionError`，**逐条**列出全部违规（`Violations`），每条带独立
类别（`Kind`，可用 `ee.Has(kind)` 判定），不会混成一句笼统的失败：

| ViolationKind | 触发条件 |
|---|---|
| `ViolFieldNumberReused` | 字段编号被回收复用（原字段的编号安给了另一个字段） |
| `ViolFieldTypeChanged` | 同一字段的类型被修改 |
| `ViolFieldRemoved` | 在仍有效的版本区间（与新消息支持区间有交集）内删除字段 |
| `ViolRangeNarrowed` | 消息或字段的版本区间被收窄；消息被整体删除 |
| `ViolCompatReversed` | 原有兼容性声明被删除，或 compat/incompat 被反转 |

命令行用法（审查不通过时**不产出任何生成结果**）：

```bash
schemagen -schema newshop.idl -old published.idl -out gen/shop
```

退出码（CI 可直接据此判定拦截原因）：

| 退出码 | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 一般失败（IO、语法、schema 校验、写盘；错误信息中含可区分类别） |
| 2 | 用法错误 |
| 3 | 协议演进审查未通过 |

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
   按原顺序**逐字节**写回（含标签与载荷的原始字节，对端未用最紧凑写法编码的
   字节也原样保留）——旧运行时中转新数据不丢字段、不变字节。
4. **损坏数据拒绝**：截断、长度越界、非法标签、varint 溢出等返回
   `wire.ErrCorrupt`。
5. **资源上限**：解码可对单条报文字节数、嵌套消息层数、单条消息字段个数设限，
   超限返回 `wire.ErrLimitExceeded`（见下节）。
6. 三类错误可用 `errors.Is` 区分：

```go
err := msg.Decode(data)
switch {
case errors.Is(err, wire.ErrIncompatibleVersion): // 版本不兼容
case errors.Is(err, wire.ErrCorrupt):             // 数据损坏
case errors.Is(err, wire.ErrLimitExceeded):       // 超出资源上限
}
```

注意：本格式是自描述的，落在字段边界上的截断前缀本身是一条合法的更短消息；
只有截断落在字段编码内部才会被判定为损坏。

## 解码资源上限

面向外部对端的解码入口建议带上限，防止超大/超深/超宽报文耗尽资源：

```go
err := msg.DecodeLimits(data, &wire.Limits{
    MaxBytes:  1 << 20, // 单条报文最大字节数（顶层解码时检查）
    MaxDepth:  8,       // 嵌套消息最大层数（顶层为第 1 层）
    MaxFields: 256,     // 单条消息最大字段个数（含未知字段）
})
```

- 各维度为零表示不限制；`Decode(data)` 等价于 `DecodeLimits(data, nil)`。
- 超限返回 `wire.ErrLimitExceeded`，与 `ErrCorrupt`、`ErrIncompatibleVersion`
  可区分，上层可按类别限流/告警。
- 上限在创建解码器时拷贝，每个解码器持有独立计数：多条报文并发解码时
  各自的配置互不串扰。
- 上限以内的正常数据行为与无上限完全一致（跨版本解码、未知字段保留均不受影响）。

## 跨版本中转：严格版本解码与字节级保真

网关类中转方使用生成的 `DecodeStrict` / `DecodeStrictLimits`：

```go
var gw oldshop.User
if err := gw.DecodeStrict(upstream); err != nil { /* ... */ }
downstream, err := gw.EncodeVersion(2) // 与 upstream 逐字节一致
```

- **严格版本**：仅把在“数据自带版本”下确实有效（`since <= v <= until`）的字段
  当作已知字段，其余一律按未知字段处理。
- **字节级保真**：未知字段重新编码时逐字节还原原始编码（标签 + 载荷），
  包括非紧凑 varint 写法；下游拿到的与上游发出的完全一致。
- 普通 `Decode` 保持宽松行为（按编号解析所有已知字段），既有用法不受影响。

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
go test ./... -race   # 含竞态检测（覆盖并发生成/并发解码用例）
go test ./testfix/ -v # 跨版本集成测试，日志含输入、判定依据与错误归类
```

演进审查流程本地验证：

```bash
go build -o /tmp/schemagen ./cmd/schemagen

# 1. 安全演进：退出码 0，正常产出
/tmp/schemagen -schema testfix/newshop.idl -old testfix/oldshop.idl -out /tmp/evo_out; echo $?

# 2. 不安全演进：退出码 3，逐条列出违规类别，不产出任何文件
#    （可改一份 idl 复用字段编号 / 删 compat 声明后重跑）

# 3. 生成产物不可编译：退出码 1，错误类别为 [生成产物不可编译]
```

测试覆盖：跨版本往返、未知字段保留（逐字节一致）、非紧凑编码透传、严格版本
解码、资源上限拒绝与并发隔离、截断/越界数据、版本不兼容、演进审查各类违规、
schema 各类校验失败、生成产物不可编译拒绝、生成确定性、并发生成无污染、
写盘失败无半成品。

重新生成测试夹具：

```bash
go run ./cmd/schemagen -schema testfix/oldshop.idl -out testfix/oldshop
go run ./cmd/schemagen -schema testfix/newshop.idl -out testfix/newshop
```
