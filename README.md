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

批量撤回使用 `POST /api/v1/components/{id}/releases/withdraw`，请求为 `{"versions":["1.0.0","1.1.0"]}`，版本须属于同一组件且不重复。每个版本按与单个撤回相同的规则检查：已被环境已解析集合或 ready 方案选用的版本不能撤回。任一版本被阻止时整批拒绝，响应 409 且 `conflicts` 逐条列出被阻止版本及原因，所有版本保持不变；全部通过时一次提交生效，目录修订号递增一次，每个版本各记录一条 withdrawn 事件。响应包含撤回后的 `releases` 与新的 `catalog_revision`。

## 接口索引

| 方法与路径（业务路径前缀 /api/v1） | 用途 |
| --- | --- |
| GET、POST /components | 分页查询、创建组件 |
| GET /components/{id} | 获取组件详情 |
| GET、POST /components/{id}/releases | 按版本降序分页查询、添加不可变版本 |
| POST /components/{id}/releases/{version}/withdraw | 使用空对象请求撤回未使用版本 |
| POST /components/{id}/releases/withdraw | 批量撤回同一组件的多个版本，全部成功或全部不变 |
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
- 错误格式为 `{"error":{"code":"...","detail":"...","conflicts":[]}}`。无解时 `conflicts` 最多给出 8 条搜索中遇到的约束证据，并非完整不可满足证明；批量撤回被拒时 `conflicts` 逐个列出被阻止的版本及各自原因。
- 400 表示输入错误；404 表示对象不存在；409 表示状态或修订号冲突；413 表示请求过大；415 表示媒体类型错误；422 表示无解或预算耗尽；503 表示繁忙。内部持久化错误返回 500，不暴露磁盘路径。

## 数据与恢复

服务使用进程独占锁，两个进程不能共享同一数据目录。状态在 `state.json` 中保存，写入临时文件并执行 fsync 后原子替换。替换成功才更新内存；目录 fsync 尽力执行，因此极端断电持久性仍取决于宿主文件系统。数据上限 64 MiB。

启动会校验 schema、引用关系、选择结果与事件序号，损坏数据会使服务拒绝启动。可在服务停止后复制整个数据目录作备份，并在停止状态下恢复。状态格式当前为 schema 1，不包含跨版本迁移机制。

## 验证与测试边界

```sh
python3 checks/workflow.py catalog
python3 checks/workflow.py resolve
python3 checks/workflow.py upgrade
```

这些是有界运行检查：启动临时 HTTP 服务、构造最小输入、验证公开 API 输出并清理数据。覆盖持久化重启、输入拒绝、版本撤回、回溯、兼容环、无解、方案验证、目录过期、环境过期、应用及取消。

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
