# AGENTS.md

本文档供在本仓库工作的 AI Agent 使用。开始修改前，先阅读本文件及目标目录附近的 README、Proto 和测试。

## 项目定位

Plato 是一个 Go 编写的分布式 IM 示例项目，Go module 为 `github.com/hardcore-os/plato`。仓库构建出一个 `plato` 二进制，通过 Cobra 子命令启动不同进程：

```text
client -> TCP -> gateway -> gRPC -> state -> Kafka -> domain/message -> MySQL
                    ^          |                       |
                    |          +-> Redis               +-> state -> gateway -> client
                    |                                      ^
                    +--------------- gRPC -----------------+

friend/group/user <-> gRPC；etcd 负责服务注册、发现和 state 分片节点监听
```

- `gateway`：TCP 长连接接入、拆包/封包、连接表、epoll 和工作池；不处理业务语义。
- `state`：连接状态、登录/重连、心跳、上行幂等、会话序号、ACK 与超时重推；按 did 分片。
- `domain/message`：消费 Kafka、消息幂等落库、单聊/群聊在线推送，以及离线消息查询。
- `domain/friend`、`domain/group`、`domain/user`：好友、群组、账号认证与设备绑定领域服务；好友和群通知可走轻量直推。
- `common`：协议、配置、缓存、消息队列、路由、服务发现、PRPC、SDK、TCP 编解码等共享能力。

## 目录与所有权

| 路径 | 职责 | 修改注意事项 |
| --- | --- | --- |
| `main.go`, `cmd/` | 单二进制入口和 Cobra 子命令 | 新服务需要同时补子命令、配置和部署定义 |
| `gateway/` | TCP 接入层和 Gateway gRPC 服务 | Linux `epoll` 实现，不要在此加入领域逻辑 |
| `state/` | 有状态连接层和消息状态机 | 保持同一 did 始终路由到同一 state 分片 |
| `domain/*/rpc/service/` | 领域 gRPC 接口与实现 | `.proto` 是接口事实来源，`*.pb.go` 是生成文件 |
| `domain/*/storage/` | GORM DAO 和存储操作 | 数据库结构体的每个字段都必须有中文含义注释 |
| `common/idl/` | 跨模块共享 Proto DTO | 修改会影响客户端、Kafka 消息和多个服务 |
| `common/prpc/` | 基于 gRPC + etcd 的 RPC 框架 | 普通无状态调用按服务名发现并负载均衡 |
| `common/gwclient/` | 精确连接路由 RPC | 用于必须命中特定 gateway/state 实例的调用 |
| `common/router/` | Redis 中的 did -> gateway/conn 路由 | 路由缺失通常表示用户离线，不应当作系统错误 |
| `common/stateroute/` | etcd 节点集 + 一致性哈希 | 跨服务访问连接状态时统一复用，禁止另写分片算法 |
| `client/`, `guiclient/`, `common/sdk/` | 终端客户端、GUI 客户端和客户端 SDK | 协议变化需同步检查三处 |
| `ipconf/`, `perf/` | IP 调度示例和压测入口 | 不属于核心消息写链路 |

## 核心消息链路

1. 客户端发送 `dataLen + MsgCmd`；`gateway` 解包后按 did 的一致性哈希精确转发到 `state`。
2. `state` 校验连接内 `ClientID`，用 Redis `INCR` 分配会话 `seq`，以 `conversationID` 为 Kafka key 同步生产消息；生产成功后才返回 UP ACK。
3. `domain/message/consumer` 消费消息，以 `(conversation_id, client_msg_id)` 唯一约束幂等落库；同一会话因 Kafka key 相同而保持分区内顺序。
4. 单聊按接收方 userID 查询其全部绑定 did；每个在线 did 再查 Redis 路由和目标 state 分片，由 state 纳入 ACK/重推状态机，最后精确调用持有连接的 gateway。
5. 离线用户不单独写离线队列；消息已在 MySQL 落库，客户端通过 `PullOfflineMsgs` 按 `seq` 补拉。已读进度存放在 Redis Hash。

## 账号、设备与会话语义

- 登录身份由“账号密码 -> session token -> userID”确定，`did` 只标识该账号下的一台设备，不能再把 did 当作账号。
- 一个 userID 可以绑定多个 did；单聊消息按接收方 userID 查出全部 did，并分别向每个在线设备推送。
- 在线路由是 `did -> gateway endpoint + connID`，存放在 Redis；连接可以分布在不同 gateway 实例，推送必须按路由精确命中持有连接的实例。
- 已读进度按 did 隔离：`read_seq_{did}` 是 Redis Hash，field 为 conversationID，value 为 readSeq。一个设备已读不能推进同账号其他设备的 readSeq。
- 消息只按 conversationID 保存一份，不能按 userID 或 did 复制消息正文。单聊双方共享 `common/convid.Single` 生成的会话 ID，群聊共享 `common/convid.Group` 生成的会话 ID。

## 消息拉取现状

当前尚未实现最近消息 Redis ZSET，消息正文的事实来源和实际拉取来源都是 MySQL：

1. SDK 调用 `GetMaxSeq`，从 Redis `msg_seq_{conversationID}` 读取服务端最大序号，并读取当前 did 的 readSeq。
2. SDK 取 `max(localSeq, readSeq)` 作为 fromSeq；若落后则调用 `PullOfflineMsgs`，默认每页 50 条。
3. message 服务执行 `WHERE conversation_id = ? AND seq > ? ORDER BY seq ASC LIMIT ?`，按 seq 升序从 MySQL 返回。
4. 实时推送发现 `seq > localSeq + 1` 时，SDK 异步补拉缺口；离线补拉与缺口补拉共用同一 RPC。
5. GUI 对同一会话按 seq 二分插入并按 seq 升序展示，重复 seq 不重复显示；网络到达顺序不要求等于展示顺序。

注意：`GetMaxSeq` 读取的是分配进度，不一定等于数据库已落库的最大 seq。seq 在 Kafka 生产前分配，生产失败可以留下空洞，拉取逻辑不能假设 seq 连续。

## 最近消息缓存决策（待实现）

计划为每个会话缓存最近 100 条消息，以加速 `lastSeq -> 增量消息`，但实现前必须保持以下设计：

- 使用一个会话级 ZSET：`recent_msg_{conversationID}`。score 为 seq，member 为序列化后的完整 `ChatMsgDTO`；不要把 userID、did 或 seq 拼进独立消息 Key。
- 通过 Lua 原子执行 `ZADD` 与 `ZREMRANGEBYRANK`，稳定只保留最新 100 条；重复消费同一 seq 时写入应幂等。
- consumer 必须先完成 MySQL 幂等落库，再写 Redis；两者成功后才能提交 Kafka offset。重试时即使数据库记录已存在，也要再次幂等写缓存，但只有本次新落库时才做在线推送，避免重复推送。
- 拉取时仅当请求范围完整落在缓存窗口内才从 ZSET 返回；缓存缺失、数据不完整或 fromSeq 早于窗口都必须回源 MySQL。MySQL 永远是事实来源，Redis 故障不能导致消息丢失。
- 不为这条链路引入 DTM，也不使用 `dtm-labs/rockscache` 管理消息 ZSET。RocksCache 的核心是字符串/Hash 形式的 `Fetch`、`FetchBatch`、`TagAsDeleted`，适合用户资料或群资料等对象缓存，不提供 ZSET 范围查询与裁剪语义。

## 不可破坏的不变量

- `gateway` 只做接入和转发；连接状态及可靠性逻辑归 `state`，领域数据归 `domain/*`。
- 上行成功 ACK 必须晚于 Kafka 生产确认，否则会出现客户端认为成功但消息丢失。
- `conversationID` 的构造必须复用 `common/convid`；单聊双方必须得到相同 ID，群聊格式必须稳定。
- 精确连接调用必须使用 `common/gwclient`；普通无状态 RPC 才使用 `common/prpc` 的服务级负载均衡。
- Kafka 是 at-least-once 语义；修改消费逻辑时必须同时检查数据库幂等和重复推送风险。
- `ClientID` 仅用于单连接顺序，`ClientMsgID` 才用于跨重连幂等；不要混用。
- `connID` 使用“版本 + 毫秒时间戳 + gateway 节点 ID + 毫秒内序号”编码；节点 ID 通过 etcd `/plato/gateway/conn-id-nodes/` 租约抢占。租约失效后 gateway 必须停止发号，禁止改回仅进程内唯一的 ID。
- `seq` 允许因 Kafka 生产失败产生空洞；客户端依赖序号不连续触发补拉。
- 客户端展示必须按会话 seq 排序，不能依赖 TCP、gRPC、Kafka 消费或 goroutine 的实际到达顺序。
- 消息拉取接口必须校验当前账号确实属于目标单聊或群聊，不能仅凭 conversationID 返回消息。
- 配置键集中通过 `common/config` 读取；同一配置值在多处使用时先赋给变量或调用现有 getter，不要重复写死。

## 开发命令

项目声明 Go `1.25.0`，本地默认配置为 `plato.yaml`，容器配置为 `plato.docker.yaml`。

```bash
# 编译单一二进制
go build -o plato .

# 查看或启动子命令
go run . --help
go run . --config ./plato.yaml gateway
go run . --config ./plato.yaml state
go run . --config ./plato.yaml message
go run . --config ./plato.yaml friend
go run . --config ./plato.yaml group
go run . --config ./plato.yaml user domain

# 启动容器环境；客户端需按界面类型单独运行
docker compose up --build
docker compose run --rm client
docker compose --profile gui up -d guiclient
```

`docker-compose.yml` 默认启动 etcd、Redis、MySQL、Kafka、gateway、state、message、friend、group、user；`client` 和 `guiclient` 分别由 `client`、`gui` profile 按需启动，`ipconf` 未加入 Compose。GUI 是原生 TCP/Fyne 客户端，浏览器通过本机 `6080` 端口访问带中文输入桥接的 noVNC 页面；原始 VNC 端口 `5900` 仅作兼容入口。不要假设本地配置和容器配置的地址可互换。

## Proto 与生成代码

- 修改 RPC 或 DTO 时先编辑 `.proto`，不要手改 `*.pb.go` 或 `*_grpc.pb.go`。
- 大多数 Proto 在根目录执行其文件头注明的命令，例如：

```bash
protoc --go_out=. --go-grpc_out=. ./domain/message/rpc/service/message.proto
```

- `gateway/rpc/service/gateway.proto` 使用历史生成方式，执行前先核对文件头命令和现有输出路径。
- 提交 Proto 变更时同时提交生成文件，并检查所有调用方、客户端 SDK、Kafka DTO 的兼容性；已有字段号不得复用。

## 编码约定

- 先沿用目标包现有结构，只做完成需求所需的最小改动；共享逻辑只有在至少两个真实调用方需要时才下沉到 `common`。
- Go 文件执行 `gofmt`；错误应携带操作上下文，正常的“用户离线/路由不存在”分支不要制造告警或 panic。
- 新增 goroutine、channel、timer、RPC 连接或 consumer 时，明确其所有者、退出条件和阻塞行为。
- 新增或修改 GORM schema/DAO 时，每个字段都写中文注释说明业务含义；同时说明唯一索引、顺序或幂等用途。
- 测试优先放在被修改包内；存储测试使用现有 `NewStorageManager(true)`/SQLite 模式，避免依赖共享开发数据库。
- 不提交本地 IDE、`.codegraph/`、临时二进制、日志或误生成的目录；先用 `git status --short` 检查范围。

## 验证要求

按改动范围从小到大执行：

```bash
go test ./path/to/changed/package
gofmt -w path/to/changed.go
git diff --check

# 以下全量命令在 Linux/Docker 且依赖服务已启动后执行
go test ./...
go vet ./...
go build -o /tmp/plato .
```

- `gateway` 直接使用 Linux epoll API；macOS 上运行 `go test ./...` 或 `go build .` 会在 `gateway/epoll.go` 报 `unix.Epoll*` 未定义，最终全量构建必须在 Linux 或 Docker 中完成。
- `common/discovery` 和部分 `common/prpc` 测试会连接 etcd；执行相关测试前先启动依赖，例如 `docker compose up -d etcd`，并给外部依赖测试设置合理超时。
- 当前 `go vet ./...` 基线还会在 `common/prpc/interceptor/server/recovery_interceptor_test.go:15` 报 `unreachable code`；不要把该既有问题误归因于无关改动。
- 仅文档改动至少检查 Markdown 内容、命令与真实入口一致，并运行 `git diff --check`。

## 常见改动落点

1. 新增消息字段/类型：修改 `common/idl/**/*.proto` -> 重新生成 -> 更新 `state` 分发 -> 更新 consumer/storage -> 更新 SDK/客户端 -> 加兼容性测试。
2. 新增领域 RPC：修改 `domain/<name>/rpc/service/*.proto` -> 生成代码 -> 实现 service -> 在 `server.go` 注册 -> 补配置和调用端。
3. 修改在线路由或分片：同时检查 `gateway/rpc/client`、`common/stateroute`、`common/router`、`state` 和 `domain/message/consumer`。
4. 修改消息可靠性：同时验证生产 ACK 时机、Kafka 幂等落库、下行 ACK 重推、断线重连和离线补拉。
5. 修改部署配置：同步检查 `plato.yaml`、`plato.docker.yaml`、`docker-compose.yml`、`Dockerfile` 中的服务名、端口和可解析地址。
