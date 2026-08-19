# 多阶段构建：先在带完整工具链的builder镜像里编译出单一的plato二进制
# (gateway/state/message/friend/group/user/client/ipconf/perf 都是同一个二进制的子命令)，
# 再拷贝到精简的运行时镜像里。用alpine而非debian是为了减小镜像体积/下载量。
#
# 注意：仓库里有的DAO包(domain/user/storage等)引入了 gorm.io/driver/sqlite(mattn/go-sqlite3)，
# 这是一个cgo库，即使运行时用的是mysql驱动，编译期仍然需要cgo工具链，所以这里显式开启CGO_ENABLED=1
# 并安装gcc/musl-dev。musl libc不像glibc那样默认提供pread64/pwrite64/off64_t这些大文件
# 偏移量相关符号，go-sqlite3的C代码里直接用到了它们，所以额外加CGO_CFLAGS定义_LARGEFILE64_SOURCE
# 让libc头文件把这些符号暴露出来，否则在alpine下编译不过。
FROM golang:1.25-alpine AS builder

WORKDIR /src
ENV CGO_ENABLED=1 GOTOOLCHAIN=auto CGO_CFLAGS="-D_LARGEFILE64_SOURCE"

RUN apk add --no-cache gcc musl-dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/plato .

# 运行时镜像：alpine本身自带ca-certificates所需的基础，装一下即可
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /out/plato /app/plato
COPY plato.docker.yaml /app/plato.yaml

ENTRYPOINT ["/app/plato"]
