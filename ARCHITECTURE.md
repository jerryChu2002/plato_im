# Plato IM 系统架构说明

> 本文档基于对代码仓库 `/Users/peropero/plato_im` 的实际代码走读整理而成(不含 `ipconf` 服务)。所有结论均标注了源码文件路径与行号,便于核查。项目为 Go 语言实现的教学向 IM(即时通讯)系统,采用微服务架构。

## 目录

1. [整体架构总览](#1-整体架构总览)
2. [服务清单与端口配置](#2-服务清单与端口配置)
3. [服务端启动流程](#3-服务端启动流程)
4. [服务发现与 RPC 框架(prpc)](#4-服务发现与-rpc-框架prpc)
5. [消息协议格式](#5-消息协议格式)
6. [客户端连接全流程](#6-客户端连接全流程)
7. [服务端如何接收客户端消息](#7-服务端如何接收客户端消息)
8. [服务端如何推送消息给客户端](#8-服务端如何推送消息给客户端)
9. [连接状态管理与可靠性保障](#9-连接状态管理与可靠性保障)
10. [已知问题与未完成项(供参考)](#10-已知问题与未完成项供参考)

---

## 1. 整体架构总览

项目采用"**接入层(gateway)/ 状态层(state)/ 领域层(domain-user)**"三层微服务架构,服务间通过自研 `prpc`(grpc 封装)框架通信,并统一注册到 etcd 做服务发现。核心设计思想是"**连接管理和业务状态分离**":gateway 只负责海量 TCP 长连接的接入与收发字节流,不理解业务;state 负责连接的登录态、心跳、消息可靠投递等"状态机"逻辑;domain/user 是纯粹的用户资料领域服务。

```
                          ┌───────────────┐
        TCP长连接(8900)   │               │  gRPC(8901,业务方向: state -> gateway)
   客户端 ───────────────▶│    gateway    │◀──────────────────────────┐
        (自定义TLV协议)   │  (epoll+ants) │                            │
                          └───────┬───────┘                            │
                                  │ gRPC SendMsg/CancelConn             │
                                  │ (固定端点 127.0.0.1:8902)           │ gRPC Push/DelConn
                                  ▼                                    │ (固定端点 127.0.0.1:8901)
                          ┌───────────────┐                    ┌───────┴───────┐
                          │     state     │────────────────────▶  state自身逻辑  │
                          │ (连接状态机)   │  (推送/重推决策)    └───────────────┘
                          └───────┬───────┘
                                  │ Redis (连接状态/登录槽/消息重发缓存/路由表)
                                  ▼
                          ┌───────────────┐        ┌───────────────┐
                          │     Redis     │        │  domain/user  │  (当前未与登录/消息链路打通,
                          └───────────────┘        │  (MySQL,资料)  │   独立的用户资料CRUD服务)
                                                    └───────────────┘

     全部服务(gateway/state/domain-user)统一注册到 etcd 做服务发现(仅注册,gateway<->state
     互调目前走配置文件写死的固定端点,未真正使用动态发现)
```

**核心结论(贯穿全文)**:当前代码中 gateway 与 state 之间的 RPC 互调,**双方都是通过 `plato.yaml` 里写死的对端 `ip:port`(`DialByEndPoint`)直连**,并没有使用 etcd 动态服务发现 + 负载均衡(该能力在 `common/prpc` 框架里是齐备的,但业务代码目前没有启用)。

---

## 2. 服务清单与端口配置

来源:`plato.yaml`、`cmd/*.go`

| 服务 | 启动命令 | 作用 | 关键端口 | 配置段 |
|---|---|---|---|---|
| gateway | `plato gateway` | TCP 长连接接入层 + 网关侧 RPC | TCP: `8900`(客户端连接) / RPC: `8901`(供 state 调用 Push/DelConn) | `gateway.*` |
| state | `plato state` | 连接状态机 / 消息可靠推送中枢 | RPC: `8902`(供 gateway 调用 SendMsg/CancelConn) | `state.*` |
| domain/user | `plato user domain` | 用户资料 CRUD 领域服务(与登录链路暂未打通) | RPC: `8903` | `user_domain.*` |
| client | `plato client` | 演示用终端聊天客户端(基于 gocui) | 直连 gateway 的 `8900` | 无独立配置段,复用 `gateway.tcp_server_port` |
| （etcd） | 外部依赖 | 服务发现注册中心 | `localhost:2379` | `discovery.endpoints` / `prpc.discov.endpoints` |
| （Redis） | 外部依赖 | state 服务的连接状态/登录槽/消息重发缓存 | `127.0.0.1:6379` | `cache.redis.endpoints` |
| （Jaeger,未生效) | 外部依赖 | 链路追踪(配置存在但入口未调用,详见第10节) | `14268` | `prpc.trace.*` |

各服务命令行入口统一挂在 `cmd/plato.go` 的根 cobra 命令下(`cmd/gateway.go:12-19`、`cmd/state.go:12-19`、`cmd/user.go:13-25`、`cmd/client.go:12-19`),真正的 `main.go` 只有一行 `cmd.Execute()`(`main.go:1-7`)。所有服务(除 client)启动时第一步都是 `config.Init(path)` 把 `plato.yaml` 加载进全局 `viper` 单例(`common/config/config.go:9-15`)。

---

## 3. 服务端启动流程

### 3.1 gateway 启动流程(`gateway/server.go` 的 `RunMain`)

```
① config.Init(path)                  加载 plato.yaml
② net.ListenTCP(":8900")             监听客户端长连接端口(gateway.tcp_server_port)
③ initWorkPoll()                     初始化全局 ants 协程池(容量 = gateway.worker_pool_num = 1024)
④ initEpoll(ln, runProc)             初始化 epoll 池:
                                        - 起 runtime.NumCPU() 个 goroutine 并发 AcceptTCP
                                        - 起 gateway.epoll_num(=4) 个 goroutine,每个持有一个独立 epoll 实例
                                        - 注册 runProc 作为"连接可读事件"回调
⑤ cmdChannel = make(chan, 2048)      创建下行指令通道(gateway.cmd_channel_num)
⑥ prpc.NewPServer(...)               构造 gateway 侧 rpc server(服务名 plato.access.gateway)
⑦ RegisterService(Gateway service)   把 Push/DelConn 接口挂载到 grpc.Server,并注入 cmdChannel
⑧ client.Init()                      初始化"gateway -> state"的rpc客户端(DialByEndPoint 固定连 127.0.0.1:8902)
⑨ go cmdHandler()                    启动"消费下行指令"协程
⑩ s.Start(ctx)                       监听 8901、grpc.Serve、注册自身到 etcd、阻塞等待退出信号
```
(`gateway/server.go:22-50`)

### 3.2 state 启动流程(`state/state.go` 的 `RunMain`)

```
① config.Init(path)         加载配置
② client.Init()             初始化"state -> gateway"的rpc客户端(DialByEndPoint 固定连 127.0.0.1:8901)
③ InitTimer()               初始化时间轮(common/timingwheel),供心跳/重连/消息重发定时器使用
④ InitCacheState(ctx)       连接 Redis,并遍历 1025 个"登录槽"恢复重启前的连接状态(容灾)
⑤ go cmdHandler()           启动"消费 state 自己的 CmdChannel"协程(处理 gateway 转发来的上行消息)
⑥ prpc.NewPServer(...)      构造 state 侧 rpc server(服务名 plato.access.state)
⑦ RegisterService(State)    把 SendMsg/CancelConn 接口挂载到 grpc.Server
⑧ s.Start(ctx)               监听 8902、grpc.Serve、注册自身到 etcd、阻塞等待退出信号
```
(`state/state.go:18-41`)

### 3.3 domain/user 启动流程(`domain/user/server.go` 的 `RunMain`)

```
① config.Init(path)         加载配置
② client.Init()             空实现(该服务当前不主动调用其他 rpc 服务)
③ service.Init(false)       构造 StorageManager:
                                - gorm.Open(mysql...) 连接 MySQL(user_domain.db_dns)
                                - 初始化本地/远程二级缓存(cache.Manager)
                                - 初始化领域事件总线(event.Manager,监听 UserEvent 通道)
④ prpc.NewPServer(...)      构造 user 侧 rpc server(服务名 plato.domain.user)
⑤ RegisterService(User)     挂载 QueryUsers/CreateUsers/UpdateUsers 三个资料 CRUD 接口
⑥ s.Start(ctx)               监听端口、grpc.Serve、注册自身到 etcd、阻塞等待退出信号
```
(`domain/user/server.go:14-33`)

> ⚠️ 配置读取 bug:`common/config/user.go` 里 `GetDomainUserServerPoint()`/`GetDomainUserRPCWeight()` 读取的 viper key 是 `"user_dimain.service_port"`(拼写少了字母 "o"),而 `plato.yaml` 实际写的是 `user_domain.service_port`,两者不匹配导致 `viper.GetInt` 返回默认值 `0`——domain/user 服务实际会以**随机端口(`:0`)、权重 0** 启动。

### 3.4 domain/user 与 state 服务当前是否打通?

**没有打通。** state 处理上行消息的 `upMsgHandler` 里(`state/state.go:139` 附近)留有 `// TODO 这里应该调用业务层的代码` 的注释,当前实现只是把客户端发来的消息原样"回显"推回同一条连接,并未真正转发给 domain/user 或其他业务服务。domain/user 的三个 RPC 接口目前只有它自己的单元测试在调用。

---

## 4. 服务发现与 RPC 框架(prpc)

### 4.1 框架分层

- `common/prpc/server.go`:`PServer`,服务端启动壳(端口监听 + 拦截器链 + etcd 注册)。
- `common/prpc/client.go`:`PClient`,客户端连接壳,提供两种拨号方式:
  - `dial()`:走 `discov:///服务名` 的**动态服务发现**(内建 grpc 原生 `roundrobin` 负载均衡)。
  - `DialByEndPoint(addr)`:绕开发现机制,**直连写死地址**——这是 gateway↔state 当前实际使用的方式。
- `common/prpc/discov/etcd/etcd_register.go`:基于 etcd 的注册/发现实现,key 格式 `/plato/prpc/{服务名}/{ip}/{port}`,用租约(Lease)+ KeepAlive 做心跳保活,`GetService` 首次全量拉取 + `watch` 增量更新。
- `common/prpc/resolver/discov_builder.go`:把 etcd 的服务变更事件(`NotifyListeners`)接入 grpc 官方 `resolver.Builder` 接口,实现"etcd 变更 → grpc 连接池地址集合实时更新"。
- `common/prpc/interceptor/`:统一注入的拦截器——Server 端固定挂载 `Recovery`(panic恢复)+ `Trace`(链路追踪)+ `Metric`(Prometheus指标);Client 端固定挂载 `Trace` + `Metric`,`Breaker`(熔断)/`RateLimit`(限流)需业务方手动挂载。
- `common/prpc/lb/`:P2C、WRR 等高级负载均衡算法**目前只是占位空文件,未实现**,实际生效的是 grpc 原生 `roundrobin`。
- `common/prpc/trace/`、`common/prpc/prome/`:分别封装 Jaeger 上报和 Prometheus `/metrics` HTTP 端点的启动函数(`StartAgent`),但 gateway/state/user 三个服务的真实入口**均未调用**这两个 `StartAgent`,所以 `plato.yaml` 里配置的 Jaeger 地址目前不会真正生效(拦截器仍会创建 span,但落在 otel 默认的 no-op provider 上)。

### 4.2 服务注册流程(以任一服务的 `s.Start(ctx)` 为例)

`common/prpc/server.go:112-172`:

1. 构造 `discov.Service{Name, Endpoints:[{IP,Port,Weight,Enable}]}`。
2. 挂载拦截器链(`Recovery → Trace → Metric → 业务自定义`),`grpc.NewServer(...)`。
3. 执行 `register(s)` 把业务 `XxxServer` 实现注入 grpc.Server。
4. `net.Listen` + `go s.Serve(lis)`。
5. `p.d.Register(ctx, &service)`——把自身地址异步写入 etcd(`/plato/prpc/{服务名}/{ip}/{port}`,带租约续期)。
6. 阻塞监听 `SIGQUIT/SIGTERM/SIGINT`,收到后 `s.Stop()` + `p.d.UnRegister(...)` 优雅下线。

### 4.3 gateway ↔ state 实际通信方式(重点)

| 调用方向 | 使用的拨号方式 | 目标地址来源 |
|---|---|---|
| gateway → state (`SendMsg`/`CancelConn`) | `DialByEndPoint` | `plato.yaml` 的 `gateway.state_server_endpoint: 127.0.0.1:8902` |
| state → gateway (`Push`/`DelConn`) | `DialByEndPoint` | `plato.yaml` 的 `state.gateway_server_endpoint: 127.0.0.1:8901` |

两侧虽然都调用了 `prpc.NewPClient(serviceName)`(内部会触发一次 etcd 服务发现拨号),但最终真正用来发请求的连接对象都是后续 `DialByEndPoint` 单独建立的静态连接,etcd 发现拨号的结果实际被丢弃未使用。**因此当前架构隐含"单 gateway 实例 + 单 state 实例"部署假设**,尚不支持多实例下的动态路由,这是教学项目现阶段的简化实现。

---

## 5. 消息协议格式

### 5.1 传输层:TCP 粘包/拆包协议(`common/tcp/`)

客户端与 gateway **共用同一套**编解码代码(`common/tcp/coder.go`、`read.go`、`write.go`),格式为 **4 字节大端长度前缀 + 变长 Body**(类似 LTV):

```go
type DataPgk struct {
    Len  uint32   // 4字节,大端,标识Data的字节长度
    Data []byte   // 消息体(下面的 protobuf MsgCmd 序列化结果)
}
```

- 拆包(`common/tcp/read.go:11-52`):先精确读 4 字节拿到 `dataLen`,再精确读 `dataLen` 字节 body,循环读满为止(解决 TCP 半包问题);每次读都设置 120 秒读超时。
- 粘包天然规避:因为严格按"先读头再按头声明长度读体"的方式消费流,多余字节留在内核缓冲区供下一次读取,不会与当前包混淆。
- 写端(`common/tcp/write.go`):同样循环写入直到写满,解决"写不满"(short write)问题。

### 5.2 业务层协议(`common/idl/message/message.proto`)

外层信封:

```protobuf
message MsgCmd {
    CmdType Type = 1;
    bytes   Payload = 2;
}
enum CmdType {
    Login     = 0;  // 登录
    Heartbeat = 1;  // 心跳
    ReConn    = 2;  // 断线重连
    ACK       = 3;  // 确认应答
    UP        = 4;  // 客户端上行消息
    Push      = 5;  // 服务端下行推送
}
```

`Payload` 按 `Type` 再反序列化成具体消息:

| Type | 消息结构 | 关键字段 |
|---|---|---|
| Login | `LoginMsg{Head: LoginMsgHead{DeviceID}, LoginMsgBody}` | 设备ID |
| Heartbeat | `HeartbeatMsg{Head: HeartbeatMsgHead{}, HeartbeatMsgBody}` | 空心跳 |
| ReConn | `ReConnMsg{Head: ReConnMsgHead{ConnID}, ReConnMsgBody}` | 上一次断连的 ConnID,用于状态迁移 |
| UP | `UPMsg{Head: UPMsgHead{ClientID, ConnID, SessionId}, UPMsgBody}` | `ClientID` 严格递增,用于幂等去重 |
| Push | `PushMsg{Content, MsgID, SessionID}` | 服务端下发的消息载体 |
| ACK | `ACKMsg{Code, Msg, Type, ConnID, ClientID, SessionID, MsgID}` | 对以上各类消息的确认应答 |

客户端 demo(`common/sdk`)在 `UPMsg.UPMsgBody`/`PushMsg.Content` 里又内嵌了一层 **JSON** 编码的业务 `Message` 结构体,因此完整分层是:

```
[4字节长度][protobuf MsgCmd][protobuf 子消息(UPMsg/PushMsg等)][JSON业务体]
```

---

## 6. 客户端连接全流程

代码位置:`client/cui.go`(终端 UI)+ `common/sdk/api.go`、`common/sdk/net.go`(核心连接逻辑)。

### 6.1 建立连接

- `client.RunMain()`(`client/cui.go:230-277`)调用 `sdk.NewChat(ip, port, ...)`。
- **客户端目前不请求 `ipconf` 服务获取最优接入点**,而是直接用配置里的固定端口(`config.GetGatewayTCPServerPort()`,即 `8900`)+ 硬编码 IP `0.0.0.0` 拨号(`common/sdk/net.go:22-37` 的 `net.DialTCP`)。
- ⚠️ `client.RunMain()` 没有调用 `config.Init(path)`,若单独进程运行 `plato client` 且未提前加载配置,`viper` 为空,端口会读到默认值 `0`。这是该 demo 客户端的已知缺陷。

### 6.2 登录握手

`sdk.NewChat` 内部顺序(`common/sdk/api.go:41-56`):

1. `go chat.loop()` —— 先启动接收协程。
2. `chat.login()`(`api.go:145-156`)—— 同步发送 `MsgCmd{Type: Login, Payload: LoginMsg{DeviceID:123(硬编码)}}`。
3. `go chat.heartbeat()` —— 启动心跳协程。

服务端(gateway 转发给 state)处理登录后回复 `ACKMsg(Type=Login)`,客户端在 `handAckMsg`(`common/sdk/net.go:39-53`)里把服务端下发的 `ConnID` 原子存入本地,**这个 ConnID 就是后续所有上行/重连消息携带的会话凭证**。

### 6.3 收发消息(独立读协程模型)

- **读**:`chat.loop()`(`api.go:104-131`)是唯一阻塞在 `tcp.ReadData` 上的协程,解出 `MsgCmd` 后按类型分流:
  - `ACK` → 更新本地 `ConnID`
  - `Push` → `handPushMsg`(解出业务消息 + **立即回复一个 ACK 给服务端**),再塞进 `recvChan`
  - UI 层另起 `doRecv` 协程消费 `recvChan` 并渲染。
- **写**:用户在 UI 按 Enter 触发 `doSay` → `chat.Send()`(`api.go:59-73`)组装 `UPMsg{Head:{ClientID(本地自增), ConnID, SessionId}, UPMsgBody}` → `conn.send()` 走 `tcp.SendData` 同步写出。

### 6.4 心跳与重连

- 心跳:`heartbeat()`(`api.go:171-195`)每 **1 秒**发送一次 `CmdType_Heartbeat`。（实现上有个 `defer chat.heartbeat()` 递归重启的瑕疵,即便主动 `Close()` 触发退出分支,也会被 defer 重新拉起,详见第10节。）
- 重连:`chat.ReConn()`(`api.go:90-97`)清空本地去重表 → `conn.reConn()` 重新 `DialTCP` → 发送 `CmdType_ReConn{ConnID: 旧连接ID}` 通知服务端做状态迁移。demo 里用一个"10秒后强制重连"的协程(`cui.go:266-270`)来演示这条路径,并非真实的断线检测触发。

---

## 7. 服务端如何接收客户端消息

### 7.1 gateway 层:accept 连接 & 读取字节流

**网络模型**:Linux epoll(`golang.org/x/sys/unix` 直接封装系统调用,非第三方 reactor 库)+ 有限大小的 ants 协程池,而不是"一连接一协程"模型。

1. **监听**:`net.ListenTCP(":8900")`(`gateway/server.go`)。
2. **accept**:`runtime.NumCPU()` 个协程并发 `AcceptTCP`(`gateway/epoll.go:55-79`),每次 accept 后 `checkTcp()` 判断是否超过 `gateway.tcp_max_num`(70000)上限,超限直接关闭新连接;`SetKeepAlive(true)` 打开 TCP 层保活。
3. **生成连接对象**:`NewConnection(conn)`(`gateway/connection.go`)用雪花算法生成 64 位全局唯一 `connID`,并通过反射拿到 `net.TCPConn` 底层真实 fd。
4. **分配到 epoll 实例**:新连接先入一个共享 channel(`eChan`),`gateway.epoll_num`(4个)epoll 协程谁先抢到就把该连接挂到自己的 epoll 实例上(`EpollCtl ADD`,水平触发 LT 模式),同时写入两张表:`fdToConnTable`(fd→conn,epoll 场景用)和全局 `ePool.tables`(connID→conn,RPC 下行推送/踢连接查表用)。
5. **事件循环**:每个 epoll 协程 `unix.EpollWait(200ms超时)`轮询,就绪后回调注册的 `runProc` 函数。

### 7.2 gateway 层:解析消息并转发

```go
// gateway/server.go 的 runProc(简化)
func runProc(c *connection, ep *epoller) {
    dataBuf, err := tcp.ReadData(c.conn)              // 阻塞读一个完整包(内部处理粘包/半包)
    if err != nil {
        if errors.Is(err, io.EOF) {                   // 客户端断开
            ep.remove(c)                               // 从epoll摘除、清理映射表
            client.CancelConn(&ctx, gwEndpoint, c.id, nil)  // 异步RPC通知state清理状态
        }
        return
    }
    wPool.Submit(func() {                              // 提交到ants协程池异步处理,不阻塞epoll事件循环
        client.SendMsg(&ctx, gwEndpoint, c.id, dataBuf) // RPC转发原始字节给state
    })
}
```

`client.SendMsg`(`gateway/rpc/client/state.go`)通过**写死的固定地址**(`gateway.state_server_endpoint = 127.0.0.1:8902`)调用 state 的 `SendMsg` RPC 接口,请求体是 `StateRequest{Endpoint, ConnID, Data}`(`Data` 就是完整的 `MsgCmd` protobuf 字节流,gateway 不解析业务内容,纯字节透传)。

**设计要点**:epoll 事件循环协程数量很少(仅 4 个),必须保持极快返回,因此所有可能阻塞的操作(RPC 调用、写 socket)都通过 `wPool.Submit` 甩给容量 1024 的 ants 协程池异步执行,防止个别慢操作拖垮整个事件循环。

### 7.3 state 层:接收并按类型分发处理

state 的 RPC 接口只有两个(`state/rpc/service/state.proto`):

```protobuf
service state {
    rpc CancelConn (StateRequest) returns (StateResponse);   // 连接断开清理
    rpc SendMsg (StateRequest) returns (StateResponse);       // 上行报文统一入口
}
```

`SendMsg` 实现(`state/rpc/service/service.go`)不直接处理业务,而是把请求包装成 `CmdContext{Cmd: SendMsgCmd, ...}` 塞进 state 内部的 `CmdChannel`,由 `state/state.go` 的 `cmdHandler` 协程统一消费:

```
cmdHandler() 消费 CmdChannel
  → 解出 message.MsgCmd
  → 按 msgCmd.Type 分发:
      CmdType_Login     → loginMsgHandler   (维护 connID↔设备状态,写Redis登录槽+路由表,回ACK)
      CmdType_Heartbeat → hearbeatMsgHandler(重置心跳定时器)
      CmdType_ReConn    → reConnMsgHandler  (连接状态迁移)
      CmdType_UP        → upMsgHandler      (幂等去重校验后触发 pushMsg —— 当前是"回显"演示,
                                              TODO: 真正应调用领域业务层)
      CmdType_ACK       → ackMsgHandler     (停止对应的消息重发定时器,删除Redis里的last_msg)
```

`CancelConn` 实现同样是把请求塞进 `CmdChannel`(`Cmd: CancelConnCmd`),由 `cmdHandler` 取出后调用 `connLogOut` 做登出清理(删除内存状态、删除 Redis 登录槽成员、删除路由表记录)。

---

## 8. 服务端如何推送消息给客户端

这是本项目里"**推送发起方是 state,而不是 gateway 被动查询**"的关键链路,完整调用链(以上行消息触发的回显推送为例):

```
① state/state.go  upMsgHandler()
     去重校验通过 → sendACKMsg(先给客户端回一个UP的ACK)
                  → pushMsg(ctx, connID, msgID, ..., upMsg.UPMsgBody)

② state/state.go  pushMsg()
     组装 PushMsg{Content, MsgID} → proto.Marshal
     → sendMsg(connID, CmdType_Push, data)                     // 触发下行RPC
     → cs.appendLastMsg(ctx, connID, pushMsg)                  // 写入 Redis(last_msg_{slot}_{connID})
                                                                   并用时间轮启动 100ms 重发定时器

③ state/state.go  sendMsg()
     组装 MsgCmd{Type: Push, Payload: downLoad} → proto.Marshal
     → client.Push(&ctx, connID, data)                          // 见④

④ state/rpc/client/gateway.go  Push()
     走"写死的固定地址"(state.gateway_server_endpoint = 127.0.0.1:8901)
     gatewayClient.Push(ctx, &GatewayRequest{ConnID, Data})       // 真正的gRPC调用

⑤ gateway/rpc/service/service.go  Push()
     RPC handler 不做任何查表/写socket动作,只是打包
     CmdContext{Cmd: PushCmd, ConnID, Payload} → 塞进 gateway 自己的 cmdChannel
     立即返回成功(异步处理)

⑥ gateway/server.go  cmdHandler()
     for cmd := range cmdChannel {
         switch cmd.Cmd {
         case PushCmd:    wPool.Submit(func(){ sendMsgByCmd(cmd) })
         case DelConnCmd: wPool.Submit(func(){ closeConn(cmd) })
         }
     }

⑦ gateway/server.go  sendMsgByCmd()
     ep.tables.Load(cmd.ConnID)                                  // O(1) 查出 *connection 对象
     dp := tcp.DataPgk{Len: len(payload), Data: payload}
     tcp.SendData(conn.conn, dp.Marshal())                        // 真正 Write 到 net.TCPConn socket

⑧ 客户端 chat.loop() 读到 MsgCmd{Type: Push}
     → handPushMsg() 解出业务内容 → 立即回复 CmdType_ACK 给服务端

⑨ state 的 ackMsgHandler 收到 ACK(经由步骤①-⑦相同的 SendMsg 上行通道)
     → 停止 100ms 重发定时器,删除 Redis 里的 last_msg
```

**要点总结**:

- **state 是推送的"决策与可靠性中枢"**:决定何时推、推什么、ACK 超时后怎么重推;但它自己**不持有 TCP 长连接**。
- **gateway 是推送的"最终执行者"**:通过 `connID → *connection` 的内存映射表(`ep.tables`,`sync.Map`)找到具体的 socket,完成最后一步写字节。
- **RPC handler 与实际 I/O 处理解耦**:无论是 gateway 收到客户端上行数据,还是 gateway 收到 state 的 Push 指令,都先包装成 `CmdContext` 塞进 channel,再由独立的 `cmdHandler` 协程统一派发到有限大小的协程池处理,避免阻塞 RPC/epoll 的主循环。
- **可靠投递(至少一次语义)**:每条 Push 消息都会被写入 Redis(`last_msg_{slot}_{connID}`)并挂一个 100ms 定时器,客户端不 ACK 就会被反复重推,ACK 到达后才清除,详见第9节。
- **多网关场景尚未打通**:`common/router` 表虽然在登录时记录了 `did → {gateway endpoint, connID}`(`AddRecord`),但当前 state 推送时并没有查询这张表(`QueryRecord` 全项目无调用点),而是固定连到配置里写死的单个 gateway 实例——这是为将来"多 gateway 集群、任意网关登录、跨网关推送"预留的扩展点,当前版本仍是单网关假设。

---

## 9. 连接状态管理与可靠性保障

### 9.1 连接状态存储(state 服务)

**双层存储**:

- **本地内存**(进程级,重启丢失):`cacheState.connToStateTable sync.Map`(`state/cache.go`),key 为 `connID`,value 为 `*connState`,内含心跳定时器 / 重连定时器 / 消息重发定时器,以及归属的设备ID(`did`)。
- **Redis**(跨进程持久,重启可恢复):
  - `login_slot_set_{slot}`:Set 集合,成员是 `"did|connID"`,登录时 `SADD`,登出时 `SREM`。
  - `last_msg_{slot}_{connID}`:最后一次下发的 `PushMsg` 序列化数据,用于 ACK 超时重发。
  - `max_client_id_{slot}_{connID}_{sessionID}`:借助 Lua 脚本"比较并自增",实现上行消息 `ClientID` 严格递增校验(幂等去重/防重放)。
  - `gateway_router_{did}`(独立于上面的 slot 体系,`common/router/table.go`):记录 `did → "endpoint-connID"`,供未来跨网关路由查询用(当前未被推送链路消费)。

### 9.2 Slot 分片设计(`state.conn_state_slot_range = "0,1024"`)

`connID % 1025` 取模(**简单取模,非一致性哈希**),把连接状态相关 key 分散到 1025 个"槽"。Redis key 中用 `{slot}` 花括号语法(Redis Cluster hash tag),保证同一个 connID 相关的所有 key(登录槽成员、last_msg、max_client_id)落在集群同一分片上,便于未来做原子操作/水平扩展。**另一个重要用途**:state 进程重启后,`initLoginSlot` 按槽并发扫描 `login_slot_set_{slot}`,对每个 `did|connID` 调用 `connReLogin` 重建内存里的连接状态和定时器,实现"分片扫描式"的状态恢复,避免一次性全量扫描造成的性能问题。

### 9.3 心跳与超时

- **传输层兜底**:`common/tcp/read.go` 每次读操作设置 120 秒读超时;`gateway` accept 时开启 TCP `SetKeepAlive(true)`(操作系统级保活)。
- **应用层心跳**:客户端每 1 秒发一次 `CmdType_Heartbeat`;state 服务用 `common/timingwheel`(时间轮)为每条连接维护 `heartTimer`,收到心跳后 `reSetHeartTimer` 刷新超时时间,超时未收到心跳则触发登出清理。
- **消息重发**:每条 Push 消息挂一个 100ms 定时器(同样基于时间轮),ACK 未到则 `rePush` 重新从 Redis 读出 `last_msg` 再次调用 `client.Push`,ACK 到达后清除定时器与缓存,实现"至少一次"送达语义。

### 9.4 registered/discovery 小结

gateway、state、domain/user 三个服务在 `prpc.NewPServer(...).Start(ctx)` 阶段都会把自己注册进 etcd(`/plato/prpc/{服务名}/{ip}/{port}`,租约续期),进程收到退出信号时会主动 `UnRegister`。但如前文所述,gateway↔state 之间的实际调用并未使用该发现机制,而是静态配置直连。

---

## 10. 已知问题与未完成项(供参考)

以下内容是代码走读过程中发现的、可能影响正确性或反映"未完工"状态的细节,附在这里供参考,不代表本次任务要求修复:

1. **domain/user 端口配置 key 拼写错误**:`common/config/user.go` 里读取的是 `user_dimain.service_port/weight`(缺字母 "o"),与 `plato.yaml` 里的 `user_domain.*` 不匹配,导致该服务实际以随机端口、权重 0 启动。
2. **业务链路未打通**:state 的 `upMsgHandler` 目前只是把上行内容"回显"给同一连接,`state/state.go` 里明确写着 `// TODO 这里应该调用业务层的代码`,domain/user 服务尚未接入登录鉴权和消息业务处理链路。
3. **`common/router.QueryRecord` 无调用点**:路由表只写不读,多网关动态路由能力尚未接入推送链路。
4. **`common/bus/event`(事件总线)是空实现**:`Manager.Send/Receive` 都是空函数体,仅在 `domain/user/storage` 里被"预埋"调用(发送 `nil` 数据),不参与实际业务。
5. **链路追踪/监控未真正启用**:`ptrace.StartAgent()`(Jaeger)和 `prome.StartAgent()`(Prometheus `/metrics`)在 gateway/state/user 的真实启动入口均未被调用,拦截器虽一直在跑但落在 no-op provider 上。
6. **客户端心跳协程存在"退出后自动重启"的瑕疵**:`common/sdk/api.go` 的 `heartbeat()` 用 `defer func(){ chat.heartbeat() }()` 递归重建自身,即便主动 `Close()` 触发正常退出分支也会被重新拉起。
7. **`gateway/connection.go` 的 `Close()` 逻辑**:`c.conn.Close()` 返回值处理写成了无条件 `panic(err)`,而不是 `if err != nil { panic(err) }`。
8. **并发写安全**:`connection` 结构体没有写锁,`gateway` 对同一连接的并发写(上行处理与下行 Push 命令有可能交织)理论上存在竞态风险,框架层未做"每连接独立写队列+单写协程串行化"的保护。
9. **读超时后未清理连接**:`common/tcp/read.go` 的 120 秒读超时返回的错误不是 `io.EOF`,而 `gateway/server.go` 的 `runProc` 目前只显式处理 `io.EOF` 分支来清理连接映射/通知 state,超时场景下清理逻辑可能被跳过。
10. **`client/` 目录未调用 `config.Init`**:与其余服务(gateway/state/user)不同,`client.RunMain()` 不接收/不加载 `ConfigPath`,单独运行时配置可能为空。
11. **客户端不查询 `ipconf` 服务**:项目中存在独立的 `ipconf`(接入点调度)服务,但当前 `client`/`common/sdk` 均未调用它,是直连固定配置的简化测试客户端(`ipconf` 本身不在本次分析范围内)。

---

## 附录:关键源码文件索引

| 主题 | 文件 |
|---|---|
| 程序入口 | `main.go`、`cmd/plato.go`、`cmd/gateway.go`、`cmd/state.go`、`cmd/user.go`、`cmd/client.go` |
| 全局配置 | `plato.yaml`、`common/config/config.go`、`common/config/gateway.go`、`common/config/state.go`、`common/config/user.go` |
| gateway 核心 | `gateway/server.go`、`gateway/epoll.go`、`gateway/connection.go`、`gateway/workpool.go`、`gateway/table.go` |
| gateway RPC | `gateway/rpc/service/gateway.proto`、`gateway/rpc/service/service.go`、`gateway/rpc/client/state.go`、`gateway/rpc/client/init.go` |
| state 核心 | `state/state.go`、`state/cache.go`、`state/server.go`、`state/timer.go` |
| state RPC | `state/rpc/service/state.proto`、`state/rpc/service/service.go`、`state/rpc/client/gateway.go`、`state/rpc/client/init.go` |
| domain/user | `domain/user/server.go`、`domain/user/rpc/service/service.go`、`domain/user/rpc/service/user.proto`、`domain/user/storage/storage.go`、`domain/user/storage/dao.go`、`domain/user/storage/options.go` |
| 传输协议 | `common/tcp/coder.go`、`common/tcp/read.go`、`common/tcp/write.go` |
| 业务协议 IDL | `common/idl/message/message.proto`、`common/idl/domain/user/user_dto.proto`、`common/idl/base/base.proto` |
| RPC 框架 | `common/prpc/server.go`、`common/prpc/client.go`、`common/prpc/discov/etcd/etcd_register.go`、`common/prpc/resolver/discov_builder.go`、`common/prpc/lb/`、`common/prpc/trace/`、`common/prpc/prome/`、`common/prpc/interceptor/` |
| 服务发现(顶层,ipconf 专用) | `common/discovery/register.go`、`common/discovery/discovery.go`、`common/discovery/model.go` |
| 路由 & 缓存 | `common/router/table.go`、`common/cache/redis.go`、`common/cache/cosnt.go`、`common/cache/lua.go`、`common/cache/manager.go` |
| 事件总线(未完成) | `common/bus/event/mq.go`、`common/bus/event/channel.go` |
| 客户端 | `client/cui.go`、`common/sdk/api.go`、`common/sdk/net.go` |
