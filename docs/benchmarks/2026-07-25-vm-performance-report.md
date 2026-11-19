# goes VM Performance Report

> 历史报告，数据已失效。旧工具用 `Eval` 计时，Yaegi 在加载 `package main` 时已经运行了入口，因而“编译”一列包含运行耗时。另外，旧工具将第二次 goes 调用的结果误当成 Yaegi 的结果，未实际校验 Yaegi 的返回值。请以[修正后重新测量的报告](2026-11-19-yaegi-benchmark.md)为准。

## Benchmark Environment

- **Go Version**: 1.23.x
- **OS**: Windows
- **yaegi Version**: v0.16.1

## Benchmark Results (ns/op)

| Benchmark | Goes Compile | Goes Run | Goes Compile+Run | Yaegi Compile | Yaegi Run | Yaegi Compile+Run | Native (Reference) |
|-----------|--------------|----------|------------------|---------------|-----------|-------------------|-------------------|
| fibonacci | 0 | 3,871,864,500 | 4,251,909,000 | 6,799,869,700 | 16,380,736,000 | 20,519,777,600 | 15,306,000 |
| control | 0 | 33,951,500 | 38,896,000 | 56,669,100 | 117,019,300 | 174,661,000 | 1,004,000 |
| aggregate | 1,976,900 | 396,796,100 | 395,359,400 | 91,873,100 | 185,699,900 | 280,158,200 | 0* |
| map | 0 | 662,094,300 | 655,545,200 | 116,099,900 | 237,098,800 | 329,666,000 | 11,400,700 |
| range | 0 | 758,627,900 | 719,740,000 | 167,314,600 | 295,350,800 | 445,901,000 | 8,024,700 |

\* Native aggregate time is 0 due to Go compiler constant folding optimization

## Goes vs Yaegi: Performance Comparison

### Run Time (Goes vs Yaegi)

| Benchmark | Goes Run | Yaegi Run | Goes is |
|-----------|----------|-----------|---------|
| fibonacci | 3,871,864,500 | 16,380,736,000 | **~4.2x faster** |
| control | 33,951,500 | 117,019,300 | **~3.4x faster** |
| aggregate | 396,796,100 | 185,699,900 | ~2.1x slower |
| map | 662,094,300 | 237,098,800 | ~2.8x slower |
| range | 758,627,900 | 295,350,800 | ~2.6x slower |

### Compile Time (Goes vs Yaegi)

| Benchmark | Goes Compile | Yaegi Compile | Goes is |
|-----------|--------------|---------------|---------|
| fibonacci | 0 | 6,799,869,700 | **~6.8s faster** |
| control | 0 | 56,669,100 | **~56.7ms faster** |
| aggregate | 1,976,900 | 91,873,100 | **~89.9ms faster** |
| map | 0 | 116,099,900 | **~116.1ms faster** |
| range | 0 | 167,314,600 | **~167.3ms faster** |

### Compile + Run Time (Goes vs Yaegi)

| Benchmark | Goes Compile+Run | Yaegi Compile+Run | Goes is |
|-----------|------------------|-------------------|---------|
| fibonacci | 4,251,909,000 | 20,519,777,600 | **~4.8x faster** |
| control | 38,896,000 | 174,661,000 | **~4.5x faster** |
| aggregate | 395,359,400 | 280,158,200 | ~1.4x slower |
| map | 655,545,200 | 329,666,000 | ~2.0x slower |
| range | 719,740,000 | 445,901,000 | ~1.6x slower |

## Performance Analysis: Goes vs Yaegi

### 1. Compile Time: Goes is Orders of Magnitude Faster

**原因分析：**

Goes 采用**字节码编译**策略，编译流程直接将 Go 源码转换为紧凑的操作码序列：

- **Goes 编译器**：解析 → 类型检查 → 生成字节码（[eval/eval.go](file:///C:/Users/Administrator/Desktop/a/goes/eval/eval.go)）。字节码格式简单，每个指令只包含操作码和少量参数。
- **Yaegi 编译器**：解析 → 构建完整 AST → 类型推断 → 生成可执行对象。Yaegi 需要构建完整的 Go AST 并进行完整的类型检查，这是一个显著更重的过程。

**影响**：对于热更新场景，Goes 的编译延迟可以忽略不计（0-2ms），而 Yaegi 需要 56ms 到 6.8s。

### 2. Recursive Functions: Goes is ~4.2x Faster

**原因分析：**

Goes 的函数调用通过**字节码指令 `OpCall`** 直接完成（[internal/vm/vm.go](file:///C:/Users/Administrator/Desktop/a/goes/internal/vm/vm.go)），调用开销仅为：
1. 创建/复用栈帧（frame pool 机制）
2. 设置参数
3. 跳转至函数入口

Yaegi 的函数调用依赖 Go 的**反射机制**（`reflect.Value.Call`），每次调用都需要：
1. 参数类型检查和转换
2. 动态方法解析
3. 反射调用开销

对于 fibonacci 这种递归深度较大的场景，反射调用的累积开销非常显著。

### 3. Control Flow: Goes is ~3.4x Faster

**原因分析：**

Goes 的字节码设计包含**专门优化的分支指令**：

- `OpJumpIfLocalIntLtConstFalse`：直接比较局部变量与常量，避免栈操作
- `OpJumpIfLocalBitAndIntEqConstFalse`：直接执行位运算并比较，避免多次栈操作

这些指令减少了控制流密集型代码中的中间栈操作次数。

Yaegi 在解释执行时，每个条件判断都需要通过反射获取变量值，然后进行比较，开销更大。

### 4. Map Operations: Yaegi is ~2.8x Faster

**原因分析：**

Yaegi 直接使用 Go 的**原生 map 类型**，通过 `reflect.MapIndex` 和 `reflect.SetMapIndex` 操作，这些是 Go 运行时优化过的操作。

Goes 的 map 操作通过**字节码指令 `OpMapGet`/`OpMapSet`** 实现（[internal/vm/vm.go](file:///C:/Users/Administrator/Desktop/a/goes/internal/vm/vm.go)），每次操作都需要：
1. 从栈顶弹出键值
2. 通过 VM 的 map 对象查找或设置
3. 类型检查和转换

Goes 的 map 实现是在 VM 层面的封装，无法直接利用 Go 运行时的 map 优化。

### 5. Range Iteration: Yaegi is ~2.6x Faster

**原因分析：**

Yaegi 可以直接使用 Go 的**原生 range 机制**，通过反射获取迭代器。

Goes 的 range 操作需要在 VM 层面**模拟迭代过程**：

- `OpRangeInitIndexed`：初始化数组/切片迭代器
- `OpRangeNextIndexed`：获取下一个元素
- `OpRangeInitMap`：初始化 map 迭代器
- `OpRangeNextMap`：获取下一个键值对

这些指令每次调用都需要维护迭代器状态，相比原生 range 有额外开销。

### 6. Aggregate Operations: Yaegi is ~2.1x Faster

**原因分析：**

Yaegi 直接操作 Go 的**原生结构体和数组**，通过 `reflect.Field` 和 `reflect.Index` 访问，这些是优化过的操作。

Goes 的结构体和数组操作通过**字节码指令**实现：

- `OpStructFieldAddIntConst`：结构体字段加法
- `OpArrayElemAddIntConst`：数组元素加法

虽然 Goes 有专门的优化指令（如 `OpLocalAddIntConst`），但结构体和数组操作仍然需要额外的类型检查和间接访问。

## Architectural Summary

| Aspect | Goes | Yaegi |
|--------|------|-------|
| **Execution Model** | Bytecode VM | Reflective JIT |
| **Compile Speed** | 极快（字节码生成） | 较慢（完整 AST + 类型检查） |
| **Function Call** | 字节码指令直接调用 | 反射调用 |
| **Control Flow** | 专用优化指令 | 通用反射操作 |
| **Map Access** | VM 层封装 | 原生 map + 反射 |
| **Range Iteration** | VM 层模拟 | 原生 range + 反射 |
| **Struct/Array Access** | VM 层封装 | 原生类型 + 反射 |

## Results Verification

所有 benchmark 的计算结果在 goes、yaegi 和 native 之间完全一致：

| Benchmark | Result | Status |
|-----------|--------|--------|
| fibonacci | 0 | OK |
| control | 999,998,000,000 | OK |
| aggregate | 16,666,816,668,000,003 | OK |
| map | 20,833,708,337,000,000 | OK |
| range | 21,600,000 | OK |

## Benchmark Programs

### Fibonacci
测试递归函数性能：
```go
func fib(n int) int {
    if n <= 1 { return n }
    return fib(n-1) + fib(n-2)
}
func main() int {
    for i := 0; i < 100000; i++ { fib(10) }
    return 0
}
```

### Control Flow
测试简单分支和循环性能：
```go
func main() int {
    sum := 0
    for i := 0; i < 2000000; i++ {
        if i & 1 == 0 { sum += i } else { sum -= 1 }
    }
    return sum
}
```

### Aggregate
测试结构体和数组操作：
```go
type pair struct { a int; b int }
func main() int {
    p := pair{1, 2}
    xs := [4]int{1, 2, 3, 4}
    sum := 0
    for i := 0; i < 1000000; i++ {
        p.a += 1; p.b += p.a; xs[i&3] += p.a
        sum += xs[0] + xs[1] + xs[2] + xs[3]
    }
    return sum + p.a + p.b
}
```

### Map
测试 map 读写性能：
```go
func main() int {
    m := make(map[string]int); m["a"] = 1; m["b"] = 2; m["c"] = 3
    sum := 0
    for i := 0; i < 500000; i++ {
        m["a"] += 1; m["b"] += m["a"]
        sum += m["a"] + m["b"] + m["c"]
    }
    return sum
}
```

### Range
测试 slice/array/map 的 range 遍历：
```go
func main() int {
    xs := []int{1,2,3,4,5,6,7,8}; arr := [4]int{3,5,7,11}
    m := map[string]int{"a":1,"bb":2,"ccc":3}
    sum := 0
    for n := 0; n < 200000; n++ {
        for i, v := range xs { sum += i + v }
        for i, v := range arr { sum += i + v }
        for k, v := range m { sum += len(k) + v }
    }
    return sum
}
```
