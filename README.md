# schema-wire-sdk-go

由 IDL schema 驱动的版本化二进制编解码 SDK：根据 schema 生成 Go 类型定义与
编解码代码，并保证生成产物与运行时在协议持续演进时仍然互通。

## 模块结构

| 包 | 职责 |
|---|---|
| `schema` | IDL 解析（`Parse`）与语义校验（`Validate`），错误按类别可区分 |
| `review` | 协议演进审查（`Review`）：对比已发布与待发布 schema，逐条分类不安全改动 |
| `gen` | 代码生成（`Generate`/`GenerateRelease`）、可编译性预检、原子化写盘 |
| `wire` | 版本化二进制编解码运行时，未知字段字节级保留，资源上限防护，错误类别可区分 |
| `cmd/schemagen` | 命令行生成工具（支持 `-baseline` 演进审查，退出码按失败类别区分） |
| `testfix` | 两套版本化生成夹具（`oldshop`/`newshop`）与跨版本/网关/上限集成测试 |

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

## 协议演进审查（发布前）

改 IDL 之前，把**线上已发布**的 schema 与**本次准备发布**的 schema 放一起
对比；审查不通过就不能产出任何生成结果。

```go
report, err := review.Review(publishedFile, candidateFile)
if err != nil { /* 某一侧 schema 自身不合法，审查中止 */ }
if !report.Safe() {
    // report.Findings 逐条列出不安全改动，Reason 区分原因
}
```

或直接走发布入口（审查 + 校验 + 可编译性预检全部通过才产出）：

```go
files, err := gen.GenerateRelease(publishedFile, candidateFile, gen.Options{})
// 审查不通过：错误可 errors.Is(err, review.ErrReviewRejected)，
// 并用 review.AsReviewError(err) 拿到逐条报告；此时 files == nil。
```

审查只拦“会破坏线上互通”的改动；新增消息、新增字段（占用未发布编号）、
放宽版本区间等安全演进放行。**每一条不安全改动按原因分列，不混成一句
笼统的失败**：

| `review.Reason` | 触发条件 |
|---|---|
| `ReasonFieldNumberReused` | 已发布的字段编号换了名字后被重新占用（编号回收复用） |
| `ReasonFieldTypeChanged` | 同一字段（同编号）类型被修改；换名同时换类型会额外单列一条 |
| `ReasonFieldRemovedActive` | 字段在候选 schema **仍然支持**的版本区间内被删除（该版本可能仍在线上流通） |
| `ReasonVersionRangeNarrowed` | 消息版本下界抬高 / 上界压低；同名字段的 since 推后 / until 提前也归此类 |
| `ReasonCompatReversed` | 同一对版本的 `compat`/`incompat` 声明被反转 |
| `ReasonCompatRemoved` | 已发布的 `compat`/`incompat` 声明在候选 schema 中被删除 |
| `ReasonMessageRemoved` | 已发布的消息类型被整体删除 |

“字段在仍然有效的版本区间里删除”的判定：只有当旧字段的有效区间
`[since, until]` 与候选消息版本区间**仍有交集**才算危险；若该字段只在
候选已不再支持的旧版本里出现过（区间无交集），则删除不再单列，区间收窄
本身仍会报 `ReasonVersionRangeNarrowed`。

### 可编译性预检（生成前拒绝坏产物）

有些 schema 能通过解析与语义校验，但生成出来的 Go 包根本编译不过（字段名
是 Go 关键字、两个字段经 snake_case 导出后同名、字段导出名撞上 SDK 保留
方法 `Encode`/`Decode`/`UnknownFields` 等）。`Generate` 在返回产物前会：

1. 先做纯静态快检（不依赖工具链）；
2. 再在一个临时模块里（`replace` 到本 SDK，**不需要联网**）对生成包执行
   一次真实的 `go build`（结论按内容哈希缓存，可并发）。

失败返回 `*gen.UncompilableError`（可 `errors.As`），**与 schema 语义错误、
演进审查失败互相区分**，且不返回任何部分产物。

### CLI 与退出码

```bash
go run ./cmd/schemagen -schema new.idl -baseline published.idl -out ./gen
```

| 退出码 | 含义 |
|---|---|
| 0 | 成功（有 `-baseline` 时表示演进审查通过） |
| 10 | 演进审查判定改动不安全（**无任何文件产出**） |
| 11 | schema 语义校验失败 |
| 12 | schema 合法但生成包无法编译 |
| 13 | IDL 语法错误 |
| 2 | 命令行参数错误；1 | IO 错误 |

CI 可直接按退出码或 `errors.Is(err, review.ErrReviewRejected)` /
`errors.As(err, *gen.UncompilableError)` 分流。

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
2. **字段门禁（严格按数据自带版本）**：编码时仅写入在目标版本有效的字段
   （`since <= v <= until`）。**解码时只把“数据自带版本”下确实有效的字段
   当作已知字段**；编号虽然存在于 schema、但在该版本无效的字段（如 v2 数据
   里的 v3 新增字段、或 v1 数据里 since=2 的字段）一律按**未知字段**处理，
   不会错误地解析进结构体。
3. **未知字段保留**：解码时遇到的未知字段存入 `UnknownFields`，重新编码时
   按**原始出现位置交错**写回（不是统一追加到末尾），保证整报文字节一致。
4. **字节级保真**：网关中转时，未知字段保存“标签 + 载荷”的完整原始字节，
   对端使用的**非紧凑 varint、非紧凑长度前缀**都会逐字节还原；以数据自带
   版本重编码时连**版本头 varint 的原始写法**也保留。下游收到的字节与上游
   发出的完全一致。已知字段仍按标准形式重新编码（值可能已被网关注入/修改）。
5. **损坏数据拒绝**：截断、长度越界、非法标签、varint 溢出等返回
   `wire.ErrCorrupt`。
6. **资源上限拒绝**：见下一节，超限返回 `wire.ErrResourceLimit`。
7. 三类错误用 `errors.Is` 区分：

```go
err := msg.Decode(data)
switch {
case errors.Is(err, wire.ErrIncompatibleVersion): // 版本不兼容
case errors.Is(err, wire.ErrCorrupt):             // 数据损坏
case errors.Is(err, wire.ErrResourceLimit):       // 报文过大/过深/字段过多
}
```

注意：本格式是自描述的，落在字段边界上的截断前缀本身是一条合法的更短消息；
只有截断落在字段编码内部才会被判定为损坏。

## 解码端资源上限防护

外部对端可能故意发送超大或超深报文。每个生成类型都有

```go
func (m *T) Decode(data []byte) error                 // 不设限，等价于下方传零值
func (m *T) DecodeWith(data []byte, lim wire.Limits) error
```

`wire.Limits`（**纯值类型，按值传递**）三个维度：

| 字段 | 含义 |
|---|---|
| `MaxBytes` | 单条报文（每一层消息载荷）的最大字节数，进入解码前判定 |
| `MaxDepth` | 嵌套消息最大层数；`1` 表示只允许顶层、不允许嵌套 |
| `MaxFields` | 单条消息内字段个数上限（每层独立计数） |

零值字段表示该维度不设限；`wire.Limits{}` 与旧行为完全一致。
`wire.DefaultLimits()` 提供一组保守默认（64MiB / 100 层 / 每层 10000 字段）。

- 超限返回 `wire.ErrResourceLimit`，与“数据损坏”“版本不兼容”互不归属，
  上层可据此分别做限流和告警；损坏数据即使配置了上限也仍归类为 `ErrCorrupt`。
- 嵌套消息的解码会创建携带同一份上限的子解码器，深度逐层累计。
- **上限以内的正常数据行为与不设限完全一致**：跨版本解码、严格版本未知化、
  未知字段交错保留与逐字节透传都不受影响。
- 每次 `DecodeWith` 持有自己的 `Limits` 副本和计数，**多条报文在多个
  goroutine 并发解码时配置互不串扰**。

## 生成流程

```bash
# 命令行生成（首次发布，无演进基线；失败不产出文件，退出码非零）
go run ./cmd/schemagen -schema testfix/newshop.idl -out testfix/newshop

# CI/发布：带上演进基线
go run ./cmd/schemagen -schema testfix/newshop.idl \
    -baseline testfix/oldshop.idl -out testfix/newshop
```

或在代码中：

```go
f, err := schema.Parse(idl)                    // 解析
files, err := gen.Generate(f, gen.Options{})   // Validate + 可编译性预检；失败无产出
err = gen.WriteFiles(dir, files)               // 先写临时文件再统一 rename

// 发布链路（推荐）：审查通过才生成
files, err = gen.GenerateRelease(published, candidate, gen.Options{})
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
go build ./...        # 编译（含对生成夹具的编译，夹具本身就是可编译性的回归）
go vet ./...          # 静态检查
go test ./...         # 全部测试
go test ./... -race   # 含竞态检测（覆盖并发生成、并发解码各自配置隔离）
go test ./review/ ./gen/ ./testfix/ ./cmd/schemagen/ -v  # 新增行为，日志含输入、判定依据、结果与错误归类
```

测试覆盖：

- **演进审查**：每一类不安全改动（编号回收复用、类型修改、存活区间删字段、
  区间收窄、compat 反转/删除、消息删除）逐条归类；安全演进放行；同一条改动
  多原因分列；报告确定性；审查失败不产出且错误可 `errors.Is` 归类。
- **可编译性预检**：字段名是 Go 关键字、字段导出折叠同名、撞保留方法名等
  “合法 schema 但编译不过”的情形在生成前被拒绝为独立错误类别。
- **资源上限**：字节数 / 嵌套层数 / 字段数超限拒绝（端到端覆盖嵌套消息），
  错误归类为 `ErrResourceLimit`，损坏数据不归入此类；零值上限与旧行为一致；
  并发解码配置互不串扰；上限以内行为完全不变。
- **严格版本 + 字节级保真**：数据自带版本下无效的已知编号字段被未知化；
  非紧凑版本头、非紧凑 varint/长度前缀、交错字段顺序、内外层未知字段在网关
  中转后与上游报文**逐字节一致**。
- **不回归**：既有跨版本往返、未知字段保留、截断/越界拒绝、版本不兼容、
  并发生成无污染、写盘原子性。

日志风格（各用例均打印输入、判定依据与最终错误归类），例如：

```
判定依据: errors.Is(err, ErrResourceLimit)=true 且 ErrCorrupt=false -> 错误归类: 资源超限 (...)
判定: 下游收到 8100... 与上游发出的逐字节一致（顺序/非紧凑写法全部保留）
```

重新生成测试夹具：

```bash
go run ./cmd/schemagen -schema testfix/oldshop.idl -out testfix/oldshop
go run ./cmd/schemagen -schema testfix/newshop.idl -out testfix/newshop
```
