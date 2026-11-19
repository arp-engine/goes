# goes

`goes` 是一个用 Go 编写的 Go 子集虚拟机。它会解析受限的 `package main` 程序，对支持的语法子集做类型检查，随后 lowering 成字节码并交给小型 VM 执行。

## 文档约束

- 生成、补充或更新仓库文档时，统一使用中文。

## 实现约束

- 为保执行效率，vm字节码执行逻辑尽量内联，避免使用函数调用。

## 当前支持

- 从单个目录加载单包 `package main` 程序。
- 跨包引用：`import` 声明，支持相对路径导入（需通过 `-m` 指定模块根目录）。
- 基础类型：`bool`、`int`、`int64`、`float64`、`string`。
- 聚合类型：数组、切片、结构体、指针、`map`。
- 控制流：`if`、`for`、`switch`、`break`、`continue`、`return`、对 `map` / `slice` / `array` 的 `for range`。
- 函数、值接收者方法、闭包、函数值以及值接收者方法值（当前支持子集）。
- 多返回值（严格 Go 语义子集）。
- 可变参（严格 Go 语义子集）。
- composite literal 的当前支持子集包括：
  - 普通 struct / array / slice literal
  - keyed struct literal
  - nested positional composite literal
- 当前测试矩阵覆盖的内建函数：`len`、`make`、`delete`、`clear`。
- `nil` 的最小支持：可用于可 nil 类型的赋值与比较。
- hook runtime、host registry、bridge、VM traceback。
- 宿主互操作中的多返回值与可变参（bridge 仍限当前支持子集）。

## 明确不支持

- `go`、`chan`、`select`
- `goto`、`fallthrough`、`defer`
- `interface`
- 方法表达式
- 指针接收者方法值
- keyed array literal
- keyed slice literal
- 嵌入字段 / 提升方法
- VM 程序内部直接访问标准库

不支持的语法应在解析或语义检查阶段明确拒绝，而不是做近似实现。

## 运行

可以对单个 `.go` 文件或一个包含 `.go` 文件的目录运行 `goes`：

```powershell
go run ./cmd/goes ./testdata/programs/scalar
```

也可以直接运行单文件：

```powershell
go run ./cmd/goes ./path/to/main.go
```

### 多包程序

当程序包含 `import` 声明时，需要通过 `-m` 指定模块根目录，以便解析相对导入路径：

```powershell
go run ./cmd/goes -m ./testdata/programs/import_test ./testdata/programs/import_test
```

导入路径为相对路径（如 `"./lib"`），相对于导入方所在目录解析。被导入的包必须是 `package main` 以外的包名，且只有导出符号（首字母大写）可被访问。

## Hook Module Runtime

`goes` 可以编译一个模块，并通过 VM 运行时 API 直接调用指定名称的函数。这支持类似 `OnCast(state, ctx)` 的 hook 风格入口，不要求 VM 程序只能从 `main` 函数执行。

宿主互操作是显式的。宿主函数必须通过编译选项声明，通过 `runtime.HostRegistry` 注册，并在 VM 代码中按名称调用。Go 值通过 bridge 层跨越 VM 边界；当前 bridge 支持 `bool`、`int`、`int64`、`float64`、`string`，以及字段类型也属于这些支持范围的命名结构体。

VM 运行时错误会包含 VM traceback，用来展示当前活跃的 hook / 函数调用链。这个 traceback 基于 VM 自己的 frame 栈，而不是 Go goroutine 栈。

## 测试

运行完整测试：

```powershell
$env:GOCACHE='D:\work\goes\.gocache'
go test ./...
```

VM 测试还包含差分检查：测试会把样例程序改写成原生 Go wrapper，并比较原生执行结果和 VM 执行结果。

## 当前限制

- 差分执行目前只比较整数返回值。
- 原生对比会把测试程序中的 `func main() int` 改写成临时可运行的 Go 程序；它面向受控样例，不适合任意源代码格式。
- 多返回值不作为一等 tuple/object 暴露；仅支持 Go 允许的多值上下文。
- Go bridge 目前只支持基础标量和命名结构体，不支持更复杂的容器互转。
- `import` 仅支持相对路径，不支持标准库或第三方模块导入。
