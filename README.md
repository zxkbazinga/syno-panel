# Synology NAS Panel

一个面向群晖 NAS 的轻量级 Docker 资源看板。使用 Go 标准库构建，无需账号登录或额外服务；浏览器通过 SSE 接收每 2 秒更新的主机与容器数据，界面适配手机和桌面屏幕。

## 部署

在已安装 Container Manager（或 Docker）的群晖上，将本项目放到 NAS 后，在项目目录执行：

```sh
docker compose up -d --build
```

然后在浏览器访问 `http://群晖IP:5666`。也可以在 Container Manager 中通过 `compose.yaml` 创建项目。

## 使用已构建的 x86_64 镜像

如果使用预先构建的镜像包，将 `nas-panel-amd64.tar` 上传到群晖并导入 Container Manager，或通过 SSH 执行：

```sh
docker load -i nas-panel-amd64.tar
docker compose -f compose.image.yaml up -d
```

`compose.image.yaml` 使用已导入的 `nas-panel:latest`，不会在群晖上重新编译。

看板读取：

- 主机 CPU 与内存：宿主机 `/proc`。
- 存储卷：宿主机挂载表和只读宿主机根目录，展示 ext4、Btrfs、XFS、NTFS、exFAT、VFAT 文件系统。
- Docker 容器：Docker Engine API，包括 CPU、内存、网络 I/O 和磁盘 I/O。

Compose 配置将宿主机的 `/volume1`、`/volume2` 存储卷及 Docker Socket 以只读方式挂入容器，并仅读取 NAS 顶层存储卷，避免把 Docker/Btrfs 的嵌套挂载误报为独立磁盘。程序会识别 NAS 挂载表在容器内显示为 `/host/volumeN` 的路径。若 NAS 使用其他卷（例如 `/volume3`），请在对应 Compose 文件的 `volumes` 下增加 `/volume3:/host/volume3:ro`。若群晖的 Docker Socket 不在 `/var/run/docker.sock`，也需在对应的 Compose 文件中同时修改 Socket 映射和 `DOCKER_SOCKET`。

## 安全提示

本看板按需求不提供登录。请仅在可信的家庭/局域网中使用，不要将端口 5666 直接暴露到公网；如需远程访问，请通过 VPN 或具备身份验证的反向代理。

虽然 Socket 文件以只读方式挂载，Docker Socket API 本身仍允许访问 Docker Engine 的管理接口。请只运行可信的看板镜像，并限制其网络可达范围。宿主机根目录仅以只读方式挂载，用于获取各存储卷的容量信息。

## 开发与验证

```sh
go test ./...
go run .
```

本地运行时，主机指标会读取当前系统的 `/proc`；Docker 数据需要当前用户可访问 `/var/run/docker.sock`。
