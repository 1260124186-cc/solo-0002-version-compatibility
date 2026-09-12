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

## 环境锁定文件

方案应用（或环境建立）后，可对环境的**当前修订**冻结一份锁定文件。它是“当时到底用了什么”的不可变证据，不参与求解、不改变环境。

```sh
curl -s http://127.0.0.1:8092/api/v1/environments/integration/lockfiles \
  -H 'Content-Type: application/json' -d '{}'
```

锁定文件包含：

- `roots`：完整根依赖约束；
- `resolved`：精确到补丁版本的解析集合；
- `releases`：每个被选版本在锁定时刻逐字复制的完整依赖定义（`requires`、状态）；
- `reasons`：每个组件被选的依据，逐条列出约束来源（`root` 或 `父组件@版本`）及其约束；
- `catalog_revision`、`environment_revision`：锁定针对的目录修订与环境修订；
- `digest`：对除摘要外全部内容规范化计算的 SHA-256，证明文件未被篡改。

不可变性：同一环境同一修订只能锁定一次，重复锁定返回 409；服务不提供修改、覆盖或删除接口。环境升级到新修订后再次锁定，会得到不同 `id`、不同 `digest`、可按修订区分和追溯的独立记录。最多保留 2000 份。

核验与导出：

```sh
curl -s http://127.0.0.1:8092/api/v1/lockfiles/{id}/verify     # 校验摘要并给出只读漂移
curl -s http://127.0.0.1:8092/api/v1/lockfiles/{id}/export      # 导出独立 JSON 证据
curl -s http://127.0.0.1:8092/api/v1/lockfiles -H 'Content-Type: application/json' \
  -d @exported-lock.json                                        # 对外部导出件带外核验（不落库）
```

`verify` 返回的 `drift` 只用于查看，指出锁定文件与**当前**目录/环境的差异，不会改变环境、目录或已应用的结果：

- `digest_valid`：文件自身摘要是否仍一致（被篡改即为 false，带外核验对篡改件返回 400）；
- `environment_matched` / `environment_matched_revision`：当前环境的根依赖与解析集合、修订号是否仍与锁定时一致；
- `catalog_revision_matched`：目录是否仍停留在锁定时的修订；
- `catalog_matched` 与 `catalog_changes`：被锁版本是否仍存在、是否被撤回（`withdrawn`）、依赖定义是否变化（`definition_changed`）、组件或版本是否已移除（`removed`）；
- `environment_changes`：相对当前环境解析集合的 add/remove/upgrade/downgrade；
- `root_changes`：根约束的新增、移除或变化。

### 与兼容性清单、环境快照的边界

| 对象 | 是否可变 | 回答的问题 |
| --- | --- | --- |
| 兼容性清单 Catalog | 可变，随组件维护推进 `catalog_revision` | 当前有哪些可用版本、各自约束是什么 |
| 环境快照 Environment | 可变，随方案应用推进 `revision` | 这个环境现在装了什么 |
| 锁定文件 Lockfile | 不可变，只追加 | 某个环境在某个修订当时锁了什么、依据什么、针对哪版目录 |

清单继续变化、环境继续升级都不会改写历史锁定文件；差异只通过 `drift` 只读呈现。锁定文件不参与求解，也不能用来“回滚”或覆盖环境——变更环境的唯一途径仍然是方案（plan）工作流。

## 接口索引

| 方法与路径（业务路径前缀 /api/v1） | 用途 |
| --- | --- |
| GET、POST /components | 分页查询、创建组件 |
| GET /components/{id} | 获取组件详情 |
| GET、POST /components/{id}/releases | 按版本降序分页查询、添加不可变版本 |
| POST /components/{id}/releases/{version}/withdraw | 使用空对象请求撤回未使用版本 |
| POST /resolve | 求解根依赖和传递依赖 |
| GET、POST /environments | 分页查询、创建环境并求解初始集合 |
| GET /environments/{id} | 查看环境根依赖、解析集合和修订号 |
| POST /environments/{id}/lockfiles | 为环境当前修订冻结不可变锁定文件 |
| GET /lockfiles | 分页查询锁定文件（可按 environment_id 过滤） |
| POST /lockfiles | 对一份外部导出的锁定文件带外核验，不落库 |
| GET /lockfiles/{id} | 查看锁定文件原文 |
| GET /lockfiles/{id}/verify | 校验摘要并返回只读漂移报告 |
| GET /lockfiles/{id}/export | 导出独立 JSON 证据 |
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

启动会校验 schema、引用关系、选择结果、锁定文件摘要与自洽性以及事件序号，损坏数据会使服务拒绝启动。可在服务停止后复制整个数据目录作备份，并在停止状态下恢复。状态格式当前为 schema 1，不包含跨版本迁移机制；早于锁定文件功能写出的状态可直接加载，其锁定集合视为空。

## 验证与测试边界

```sh
python3 checks/workflow.py catalog
python3 checks/workflow.py resolve
python3 checks/workflow.py upgrade
python3 checks/workflow.py lockfile
```

这些是有界运行检查：启动临时 HTTP 服务、构造最小输入、验证公开 API 输出并清理数据。覆盖持久化重启、输入拒绝、版本撤回、回溯、兼容环、无解、方案验证、目录过期、环境过期、应用及取消。锁定检查覆盖同修订重复锁定拒绝、跨修订可区分、摘要篡改拒绝、环境与目录漂移只读、撤回漂移、带外核验及重启持久性。

测试故意延后：初始化基线采用 `testing=deferred`，不附单元测试、测试夹具或 E2E 测试文件，也不声明 test_command。后续工程测试任务负责补充细粒度边界、并发竞争和故障注入测试。当前冒烟检查不替代完整测试套件。

## 目录

- `cmd/server`：启动、监听及平滑退出。
- `internal/semver`：稳定语义版本与约束。
- `internal/domain`：实体、校验及状态规则。
- `internal/repository`：状态副本、持久化、独占锁和启动校验。
- `internal/resolution`：回溯求解、约束证据及版本差异。
- `internal/locking`：锁定文件的规范化摘要、生成、自洽核验与只读漂移比对。
- `internal/service`：组件、环境、方案、锁定文件与事件流程。
- `internal/httpapi`：HTTP 路由、请求边界及响应。
- `internal/config`：环境配置。
- `checks`：公开 HTTP 运行检查。

当前不提供网页界面、第三方组件源接入、多实例共享数据、方案清理接口或预发行版本支持。版本内容不可修改，撤回不可逆；采用新的版本号修订依赖。
