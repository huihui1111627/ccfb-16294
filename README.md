# Zupu · 族谱逻辑规则查询引擎

一个零外部依赖的 Go/Datalog 风格规则引擎，面向几十万族谱人物的父子、婚配、过继、迁居记录。它会自动推出递归祖先、同宗距离、断嗣支系、计数/最小值聚合等结论，并提供：

- 分层与否定/聚合安全检查；
- 半朴素（delta）增量求值；
- 每轮新增事实统计；
- 规则执行次数、候选连接数和耗时；
- 任意参数位置的内存索引与部分绑定查询；
- 每条结论的可回放推导树；
- 固定种子的大家族和随机规则集生成；
- 与“每轮全量重算”的朴素求值器逐事实对照。

## 快速开始

```bash
# 默认生成 240,000 人，固定种子 20261125，启动可视化控制台
go run ./cmd/zupu serve

# 小一点的数据规模，启动更快
go run ./cmd/zupu serve --people 20000 --addr 127.0.0.1:8090

# 命令行跑全部 11 套规则的半朴素/朴素逐条对照
go run ./cmd/zupu verify --people 240000 --set all \
  --json reports/verify-240000.json
```

打开 <http://127.0.0.1:8090>。

常用参数：

```text
--people 240000   目标人物数（根家族数量会随规模缩放）
--seed 20261125   固定随机种子
--set genealogy-core
--addr :8090
```

## 检查结果

已运行：

```bash
gofmt -w .
go test ./...
go vet ./...
go run ./cmd/zupu verify --people 240000 --set all \
  --json reports/verify-240000.json
```

240,000 人规模下生成 **794,023** 条基础事实。1 套核心规则 + 10 套固定种子随机规则全部与朴素求值器逐谓词、逐编码事实一致：

| 规则集 | 参与比较的事实数 | 半朴素候选连接 | 朴素候选连接 | 连接工作量比 |
|---|---:|---:|---:|---:|
| genealogy-core | 7,959,544 | 15,791,656 | 64,917,243 | 4.11× |
| random-01 | 3,528,271 | 6,575,105 | 39,139,958 | 5.95× |
| random-02 | 3,226,616 | 4,540,755 | 24,063,525 | 5.30× |
| random-03 | 4,630,904 | 7,671,196 | 34,789,871 | 4.54× |
| random-04 | 3,178,702 | 4,517,677 | 28,297,686 | 6.26× |
| random-05 | 2,721,014 | 3,749,948 | 19,746,593 | 5.27× |
| random-06 | 3,734,776 | 6,479,215 | 32,909,261 | 5.08× |
| random-07 | 3,523,988 | 4,863,674 | 25,975,869 | 5.34× |
| random-08 | 4,114,171 | 8,270,946 | 31,718,232 | 3.83× |
| random-09 | 3,499,570 | 5,836,332 | 31,013,455 | 5.31× |
| random-10 | 3,456,178 | 5,079,079 | 27,038,525 | 5.32× |

完整机器可读报告在 [`reports/verify-240000.json`](reports/verify-240000.json)。

> “候选连接”口径是：已有变量绑定后，访问某个关系/增量并检查的候选事实行数。它不是单纯函数调用次数；页面和 JSON 还同时给出执行次数、匹配数、索引查找、全扫描和耗时。

## 规则语法

语句以 `.` 结束，规则用 `:-` 连接头部和身体，逗号分隔条件。变量以 `?` 开头，`_` 是无名变量；同一个具名变量在一条规则中多次出现表示取值相等。

### 事实和递归

```prolog
father("p1001", "p0998").

ancestor(?x, ?y) :- father(?x, ?y).
ancestor(?x, ?z) :- father(?x, ?y), ancestor(?y, ?z).
```

支持线性递归、相互递归和多重传递闭包。随机规则集包含 official father / biological father 双闭包、相互递归和有界距离递归。

### 常量、重复变量和无名变量

```prolog
male(?p) :- person(?p, _, "M", _, _, _).

sibling(?x, ?y) :- father(?x, ?f), father(?y, ?f), ?x != ?y.
```

每个 `_` 都是独立的匿名变量；`?f` 出现两次会执行相等连接。

### 比较和内置函数

```prolog
short_dist(?x, ?z, ?d) :-
  father(?x, ?y),
  short_dist(?y, ?z, ?d0),
  let ?d = ?d0 + 1,
  ?d <= 3.

generation_char(?p, ?w) :-
  person(?p, ?name, _, _, _, _),
  let ?w = substr(?name, 1, 1).

generation_gap(?x, ?y, ?gap) :-
  ancestor(?x, ?y),
  person(?x, _, _, _, ?gx, _),
  person(?y, _, _, _, ?gy, _),
  let ?gap = ?gy - ?gx.
```

支持 `+ - * / mod abs min2 max2`，以及按 Unicode 字符截取的
`substr/substring`、`char_at`、`concat`、`length`、`upper`、`lower`、
`trim`、`contains`、`has_prefix`。

### 否定和分层

```prolog
has_son(?f) :- son(?c, ?f).
no_son(?f) :- male(?f), not has_son(?f).

male_desc(?d, ?a) :- son(?d, ?a).
male_desc(?d, ?a) :- son(?d, ?p), male_desc(?p, ?a).
has_male_desc(?a) :- male_desc(?d, ?a).
extinct_branch(?a) :- male(?a), not has_male_desc(?a).
```

引擎构建谓词依赖图和 SCC。若否定边或聚合边落在同一个递归 SCC 中，会直接返回 `unstratifiable rule set` 错误，例如：

```prolog
p(?x) :- q(?x), not p(?x).   % 编译期报错
```

### 聚合

当前采用显式、可安全分层的聚合形式：

```prolog
child_count(?f, ?n) :-
  agg(count(?c), father(?c, ?f), group(?f), ?n).

earliest_child_birth(?f, ?m) :-
  child_birth(?c, ?f, ?b),
  agg(min(?b), child_birth(?c, ?f, ?b), group(?f), ?m).
```

支持 `count`、`min`、`max`、`sum` 和 `distinct`。聚合源关系位于更低分层；聚合头必须等源关系完全闭合并完成本层迭代后才执行。

## 家族数据模型

固定种子生成约 24 万人和 80 万条基础记录：

```text
person(id, name, sex, birth_year, generation, clan)
father(child, official_father)       -- 过继后为礼法/宗族父亲
biofather(child, biological_father)
mother(child, mother)
spouse(person_a, person_b, year)
adoption(child, bio_father, adoptive_father, year)
migration(person, from_place, to_place, year)
```

生成器按字辈诗给名，因此可用 `substr(name, 1, 1)` 截取字辈；每一代都有无男嗣、外迁或未过继的支系，可推出断嗣关系。

## Web 页面

启动后访问 <http://127.0.0.1:8090>，包含五个页签：

1. **依赖图与分层**：规则节点、EDB 节点、正/否定/聚合边、SCC 分层；
2. **迭代新事实**：每轮新增数量柱状图、按谓词明细和耗时；
3. **规则执行统计**：每条规则的执行次数、候选连接、匹配、推出/新增事实和耗时；
4. **索引查询 / 推导树**：
   - 选择谓词；
   - 输入 JSON 参数数组，`null` 表示该参数不绑定；
   - 可对任意参数位置或复合参数组建索引；
   - 点击结果行查看完整推导树；
5. **朴素全量对照**：在当前数据规模上运行第二台朴素引擎，显示逐事实一致性和两种连接工作量。

推导树没有在求值时为每行持久化，而是在请求时沿已存储事实反向回放，避免给数百万结论额外保存证明对象。聚合节点可展开其来源事实。

## HTTP/JSON 接口

```bash
# 总览：报告、分层、迭代、规则统计、已有索引
curl http://127.0.0.1:8090/api/overview

# 规则依赖图
curl http://127.0.0.1:8090/api/graph

# 样例事实
curl 'http://127.0.0.1:8090/api/facts?pred=father&limit=20'

# 部分绑定查询；未绑定位置传 null。首次查询会自动建对应复合索引
curl -X POST http://127.0.0.1:8090/api/query \
  -H 'Content-Type: application/json' \
  -d '{"predicate":"ancestor","args":["p0000123",null]}'

# 给任意参数位置/复合参数组建索引
curl -X POST http://127.0.0.1:8090/api/indexes \
  -H 'Content-Type: application/json' \
  -d '{"predicate":"person","columns":[2,4]}'

# 推导树；args 是 JSON 数组的 URL 编码
curl 'http://127.0.0.1:8090/api/proof?pred=child_count&args=%5B%22p0001583%22%2C3%5D'

# 当前数据规模的朴素对照（大规模可能耗时数十秒）
curl 'http://127.0.0.1:8090/api/verify?set=genealogy-core&refresh=1'
```

## 求值模型

1. **解析**：词法/语法分析为 `Program{Facts, Rules}`；
2. **一致性检查**：检查谓词元数、变量安全性和聚合变量；
3. **分层**：依赖图 + Tarjan SCC；严格的否定/聚合环直接报错；
4. **连接规划**：优先使用常量和已绑定的共享变量，自动建单列连接索引；
5. **半朴素求值**：
   - 每层首轮从当前层空 IDB 启动，得到基础规则结论；
   - 后续为每个同层递归正文字面量生成一个 delta 变体；
   - delta 原子作为驱动表，只访问上一轮新事实；
   - 本轮结论先进入 pending，整轮结束后统一合并，保证轮次统计清晰；
6. **去重**：每个关系以长度前缀编码的完整元组为 map key，多规则推出同一事实只存一份；
7. **朴素对照**：每个分层中每轮用全量关系重算所有规则，直到新增为零；
8. **证明回放**：按规则头匹配目标，再用带索引的目标驱动搜索重建一棵具体推导树。

## 目录

```text
value.go        标量值与无歧义元组键
ast.go          规则 AST
parser.go       Datalog 风格语法解析
store.go        关系、去重 map、任意参数索引
compile.go      变量编号、连接排序、SCC 分层
evaluate.go     半朴素与朴素求值器、统计计数
functions.go    算术/字符串内置函数
proof.go        按需推导树回放
graph.go        规则依赖图
family.go       固定种子家族生成器
rulesets.go     核心规则与固定种子随机规则集
verify.go       两种求值器逐事实对照
server.go       HTTP API 与嵌入页面
web/index.html  无外部依赖的可视化控制台
cmd/zupu/       serve / verify 命令
engine_test.go  语义与随机规则差分测试
```
