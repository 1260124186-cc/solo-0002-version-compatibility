# 软件版本兼容性验证服务

一个使用 Go 标准库实现的 HTTP 后端。维护稳定组件版本及依赖约束，求解兼容版本集合，并以可验证、可撤销的升级方案更新目标环境。

## 运行

需要 Go 1.23 或更高版本，支持 Linux、macOS。服务本身没有第三方依赖，无须网络数据库。HTTP 冒烟检查还需要 Python 3。

```sh
go build -o build/compat-server ./cmd/server
go vet ./...
./build/compat-server
```

默认地址为 `http://127.0.0.1:8092`，健康端点为 `GET /healthz`。停止时使用 SIGINT 或 SIGTERM，服务将等待进行中的请求完成并释放数据锁。

| 环境变量 | 默认值 | 含义 |
| --- | --- | --- |
| COMPAT_ADDRESS | 127.0.0.1:8092 | HTTP 监听地址；端口 0 可分配空闲端口 |
| COMPAT_DATA_DIR | var/version-compatibility | 独立持久化目录 |
| COMPAT_MAX_STEPS | 50000 | 每次求解最多搜索步骤，允许 100–1000000 |
| COMPAT_REQUEST_TIMEOUT | 10s | 请求期限，允许 1s–1m |

运行日志为 stderr JSON，包括实际监听地址和请求编号。运行数据与构建产物均由 Git 忽略。服务面向受信任网络，默认仅监听回环地址；没有身份系统。

## 完整使用示例

创建两个组件，再依次添加版本。所有 POST 请求使用 `Content-Type: application/json`。

```sh
curl -s http://127.0.0.1:8092/api/v1/components -H 'Content-Type: application/json' -d '{"id":"compute-core","name":"计算核心"}'
curl -s http://127.0.0.1:8092/api/v1/components -H 'Content-Type: application/json' -d '{"id":"render-unit","name":"渲染单元"}'
curl -s http://127.0.0.1:8092/api/v1/components/compute-core/releases -H 'Content-Type: application/json' -d '{"version":"1.0.0","requires":{}}'
curl -s http://127.0.0.1:8092/api/v1/components/render-unit/releases -H 'Content-Type: application/json' -d '{"version":"1.0.0","requires":{"compute-core":"^1.0.0"}}'
curl -s http://127.0.0.1:8092/api/v1/resolve -H 'Content-Type: application/json' -d '{"roots":{"render-unit":"*"}}'
```

求解响应的 `resolved` 包含 `render-unit=1.0.0` 和 `compute-core=1.0.0`，`edges` 给出传递依赖边，`steps` 给出搜索步骤，`catalog_revision` 标记所用目录版本。求解只计算结果，不改变任何环境。

创建环境：

```sh
curl -s http://127.0.0.1:8092/api/v1/environments -H 'Content-Type: application/json' -d '{"id":"staging","name":"预演环境","roots":{"render-unit":"1.0.0"}}'
```

新增组件版本后，提交 `POST /api/v1/plans`，请求包含 `environment_id`、当前环境的 `base_revision`、完整的新 `roots` 及 `reason`。返回的方案最初为 `draft`、`revision=1`。依次调用：

1. `POST /api/v1/plans/{id}/validate`，发送 `{"revision":1}`。成功返回 `ready` 方案，其 `changes` 描述新增、移除、升级或降级；后续请求必须使用返回的新修订号。
2. `POST /api/v1/plans/{id}/apply`，发送当前方案修订号。成功同时返回更新后的 `plan` 与 `environment`。
3. 若不再需要，可对 draft 或 ready 方案调用 `/cancel`。已经应用的方案不能取消，应建立另一个方案调整环境。

根依赖始终表示完整期望集合，不是增量补丁。目录变化后，应重新验证 ready 方案。环境变化后，应使用新环境修订号建立新方案。重复应用、旧修订号及正在使用的版本撤回均返回 409。

## 组件生命周期

组件具有独立于版本的生命周期状态：

| 状态 | 新增版本 | `/resolve` 分析 | 新环境引用 | 已有环境保留与重新求解 |
| --- | --- | --- | --- | --- |
| `active` | 允许 | 允许 | 允许 | 允许 |
| `deprecated` | 拒绝 | 允许 | 允许 | 允许 |
| `retired` | 拒绝 | 允许，但只是计算结果 | 拒绝 | 已在环境中的组件允许保留；不允许新增该组件 |

状态迁移只允许：

```text
active      → deprecated
deprecated  → active      （重新激活）
deprecated  → retired
retired     → active      （显式恢复）
```

不允许 `active` 直接 `retired`；相同状态的重复写入也会被拒绝。每个组件最多保留 100 条生命周期迁移。弃用和恢复都会保留已有 Release：Release 仍为 `available` 或 `withdrawn`，组件状态不会隐式撤回版本。下线后的恢复是一个需要原因的显式动作，恢复为 `active` 后才可以添加版本并重新被新环境引用。

```sh
curl -s http://127.0.0.1:8092/api/v1/components/render-unit/lifecycle \
  -H 'Content-Type: application/json' \
  -d '{"state":"deprecated","reason":"停止增强，进入维护观察期"}'
curl -s http://127.0.0.1:8092/api/v1/components/render-unit/lifecycle \
  -H 'Content-Type: application/json' \
  -d '{"state":"retired","reason":"停止新环境接入"}'
curl -s http://127.0.0.1:8092/api/v1/components/render-unit/lifecycle \
  -H 'Content-Type: application/json' \
  -d '{"state":"active","reason":"恢复维护"}'
```

组件响应包含当前 `state`、`deprecated_at`、`retired_at` 及完整 `lifecycle` 时间线；全局事件记录 `deprecated`、`reactivated`、`retired`、`restored` 动作，并在 metadata 中保存 `from`、`to` 和 `reason`。生命周期变化属于目录变化，会递增目录修订号。

### 责任边界与交互时间线

| 机制 | 唯一负责的判断 | 不负责的判断 |
| --- | --- | --- |
| 组件生命周期 | 是否允许新增 Release；是否允许形成新的持久化组件引用 | 不修改 Release，不判断语义约束，不删除环境或历史方案 |
| Release 时间线 | 版本创建后不可变；只有未被任何环境解析集合使用的版本可撤回 | 不表示组件弃用；撤回版本不会改写方案、环境或组件状态 |
| 依赖求解 | 在 `available` Release 中按约束、确定性优先级和预算计算兼容集合 | 不读取组件生命周期；`/resolve` 只返回分析结果，不产生引用 |
| 方案验证 | 对目标根依赖重新求解、生成差异，并把组件准入落实为可应用结果 | 不隐式修改环境；不重新实现版本撤回判断 |
| 修订号与应用 | 保证 ready 方案基于当前目录修订和环境修订，且只应用一次 | 不判断业务原因；目录变化后只要求重新验证 |

典型顺序是：

1. **创建版本**：只有 `active` 组件能添加 Release；版本进入 `available`。
2. **弃用组件**：`active → deprecated`，记录 `deprecated_at`。此后不能新增版本；已有版本仍可求解，也仍可被新环境引用。
3. **撤回版本**：将单个 Release 从 `available` 改为 `withdrawn` 并记录 `withdrawn_at`。求解器随后不再选择它；正被任一环境使用时拒绝撤回。这是版本级动作，不要求、也不改变组件弃用状态。
4. **下线组件**：只能 `deprecated → retired`，记录 `retired_at`。求解仍可分析旧版本，但创建环境不能引用该组件，已有环境的方案也不能把它作为新组件加入；已经存在的环境、已应用/已取消方案以及历史撤回记录保持不变。
5. **方案失效**：组件生命周期或 Release 撤回都会改变目录修订。生命周期变化前已经 ready 的方案在应用时因目录修订不一致返回 409；重新验证时再执行一次求解和组件准入。
6. **恢复组件**：只能显式 `retired → active`，必须提供原因；恢复清除 `deprecated_at` 与 `retired_at`，之后才能添加版本或重新形成新引用。
7. **重新激活弃用**：`deprecated → active` 同样清除弃用时间并恢复新增版本能力。

因此，组件生命周期回答“这个组件还能不能继续演进、能不能形成新引用”；版本撤回回答“这个具体版本还能不能被新的求解选择”；求解器回答“哪些版本满足约束”；方案验证回答“当前目录下目标根依赖能否安全地应用到指定环境”。同一条规则只在其所属层判断一次，跨层一致性通过目录修订号强制重新求解。

## 接口索引

| 方法与路径（业务路径前缀 /api/v1） | 用途 |
| --- | --- |
| GET、POST /components | 分页查询、创建组件 |
| GET /components/{id} | 获取组件详情、生命周期状态与时间线 |
| POST /components/{id}/lifecycle | 弃用、重新激活、下线或恢复组件 |
| GET、POST /components/{id}/releases | 按版本降序分页查询、添加不可变版本 |
| POST /components/{id}/releases/{version}/withdraw | 使用空对象请求撤回未使用版本 |
| POST /resolve | 求解根依赖和传递依赖 |
| GET、POST /environments | 分页查询、创建环境并求解初始集合 |
| GET /environments/{id} | 查看环境根依赖、解析集合和修订号 |
| GET、POST /plans | 分页筛选、创建方案 |
| GET /plans/{id} | 查看方案与变更明细 |
| POST /plans/{id}/validate | 求解并生成可应用方案 |
| POST /plans/{id}/apply | 检查修订号并应用方案 |
| POST /plans/{id}/cancel | 取消尚未应用的方案 |
| GET /events | 按序号增量读取变更事件 |

集合接口接受 `offset` 与 `limit`（默认 50，最大 200），返回 `items`、`total`、`offset`、`limit`。方案可按 `environment_id`、`state` 筛选。事件接口使用 `after`、`limit`、可选 `entity_id`，返回 `next_after` 和 `latest`；事件最多保留最近 10000 条，游标早于保留范围时 `truncated=true`。

## 约束及失败行为

- 仅支持稳定 `major.minor.patch`，每段最大 4294967295，无前导零。支持 `*`、精确版本、`>=`、`<=`、`>`、`<`、`^`、`~` 及空格分隔的交集。
- `^1.2.3` 表示至少 1.2.3 且小于 2.0.0；`^0.2.3` 小于 0.3.0；`^0.0.3` 小于 0.0.4；`~1.2.3` 小于 1.3.0。
- 不支持预发行标记、构建元数据、通配数字段或 OR 表达式。依赖组件必须已存在，允许先建立组件后逐个添加含环依赖的版本。
- 最多 500 个组件、每组件 200 个版本、每个根集合或版本 32 条依赖；一次求解最多涉及 128 个组件。最多 200 个环境和 5000 个方案。
- 组件按标识字典序求解，候选版本按降序尝试，发生约束冲突时回溯。不保证全局最少变更；升级可能间接引入降级，必须查看方案差异。
- JSON 请求上限 64 KiB，拒绝未知字段、重复键、无效 UTF-8、非对象请求及额外 JSON 值。最多同时处理 32 个请求。
- 错误格式为 `{"error":{"code":"...","detail":"...","conflicts":[]}}`，`conflicts` 仅无解时出现，最多给出 8 条搜索中遇到的约束证据，并非完整不可满足证明。
- 400 表示输入错误；404 表示对象不存在；409 表示状态或修订号冲突；413 表示请求过大；415 表示媒体类型错误；422 表示无解或预算耗尽；503 表示繁忙。内部持久化错误返回 500，不暴露磁盘路径。

## 数据与恢复

服务使用进程独占锁，两个进程不能共享同一数据目录。状态在 `state.json` 中保存，写入临时文件并执行 fsync 后原子替换。替换成功才更新内存；目录 fsync 尽力执行，因此极端断电持久性仍取决于宿主文件系统。数据上限 64 MiB。

启动会校验 schema、引用关系、生命周期时间线、选择结果与事件序号，损坏数据会使服务拒绝启动。可在服务停止后复制整个数据目录作备份，并在停止状态下恢复。状态格式当前为 schema 1，不包含跨版本迁移机制；旧状态缺少新增生命周期字段时，按历史组件均为 `active` 兼容加载。

## 验证与测试边界

```sh
python3 checks/workflow.py catalog
python3 checks/workflow.py resolve
python3 checks/workflow.py upgrade
python3 checks/workflow.py lifecycle
```

这些是有界运行检查：启动临时 HTTP 服务、构造最小输入、验证公开 API 输出并清理数据。覆盖持久化重启、输入拒绝、版本撤回、组件弃用/重新激活/下线/恢复、回溯、兼容环、无解、方案验证、目录过期、环境过期、应用及取消。

测试故意延后：初始化基线采用 `testing=deferred`，不附单元测试、测试夹具或 E2E 测试文件，也不声明 test_command。后续工程测试任务负责补充细粒度边界、并发竞争和故障注入测试。当前冒烟检查不替代完整测试套件。

## 目录

- `cmd/server`：启动、监听及平滑退出。
- `internal/semver`：稳定语义版本与约束。
- `internal/domain`：实体、校验及状态规则。
- `internal/repository`：状态副本、持久化、独占锁和启动校验。
- `internal/resolution`：回溯求解、约束证据及版本差异。
- `internal/service`：组件、环境、方案与事件流程。
- `internal/httpapi`：HTTP 路由、请求边界及响应。
- `internal/config`：环境配置。
- `checks`：公开 HTTP 运行检查。

当前不提供网页界面、第三方组件源接入、多实例共享数据、方案清理接口或预发行版本支持。版本内容不可修改，撤回不可逆；采用新的版本号修订依赖。
