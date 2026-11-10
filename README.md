# Synology NAS Panel

一个面向群晖 NAS 的轻量级 Docker 资源看板。使用 Go 标准库构建，无需账号登录或额外服务；浏览器通过 SSE 接收服务端推送，Docker 容器资源直接使用 Docker Stats 持续流，主机 CPU、内存和网络指标约每秒采样，界面适配手机和桌面屏幕。

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

`compose.image.yaml` 默认使用已导入的 `nas-panel:latest`，不会在群晖上重新编译。若已推送到 Zot，可在群晖上先登录仓库，再使用仓库镜像部署：

```sh
docker login zot.zzbazinga.site:8888
NAS_PANEL_IMAGE=zot.zzbazinga.site:8888/nas-panel:latest docker compose -f compose.image.yaml pull
NAS_PANEL_IMAGE=zot.zzbazinga.site:8888/nas-panel:latest docker compose -f compose.image.yaml up -d
```

登录时按提示输入 Zot 用户名和密码，不要将凭据写入 Compose 文件。HTTPS 证书需由 Ubuntu、群晖上的 Docker 信任；若使用自签名证书，请先将 CA 证书安装到对应主机的 Docker 信任目录。

## 使用 Gitea act_runner 构建并推送到 Zot

仓库中的 `.gitea/workflows/publish-image.yaml` 会在推送到 `main` 分支或手动触发时，构建群晖 x86_64 使用的 `linux/amd64` 镜像并推送到 Zot。每次构建都会更新 `latest` 并发布以提交 SHA 命名的不可变镜像标签；符合 Conventional Commits 的版本更新还会发布 SemVer 镜像标签并创建对应的 Git 标签（格式 `v主版本.次版本.补丁版本`）。页面底部会显示镜像构建时注入的版本号。

版本升级规则（统计自上一个 `vX.Y.Z` Git 标签之后的所有提交）：

- `feat:`：次版本号 +1，补丁号归零。
- `fix:` 或 `perf:`：补丁号 +1。
- `BREAKING CHANGE:` 正文，或 `feat!:`、`fix!:`、`perf!:`：主版本号 +1，次版本号和补丁号归零。
- 多种提交同时存在时，按主版本、次版本、补丁版本的优先顺序取最高升级级别。其他提交类型只更新 `latest` 和 SHA 标签，不创建新 SemVer 版本。
- 第一次运行且仓库没有 `vX.Y.Z` 标签时，从 `v0.1.0` 开始。

例如推送 `feat: add disk alerts` 后发布 `v0.2.0` 和 `latest`；推送 `fix: correct memory usage` 后发布 `v0.2.1`。部署时可以固定镜像版本（如 `nas-panel:v0.2.1`），或使用持续更新的 `nas-panel:latest`。

在 Gitea 仓库的 **Settings → Actions → Secrets** 中添加：

- `ZOT_USERNAME`：Zot 用户名。
- `ZOT_PASSWORD`：Zot 密码或访问令牌。

工作流使用 `runs-on: ubuntu-latest`，对应 act_runner 注册标签 `ubuntu-latest:host`，步骤直接在 Ubuntu 宿主机执行。宿主机需预装 Git、Docker CLI 和 Node.js 20（供 `actions/checkout` 使用）；运行 act_runner 的系统用户需有权访问 Docker daemon（通常将该用户加入 `docker` 组），且 Docker daemon 必须信任 Zot 的 HTTPS 证书。工作流不会创建 job 容器，也不会安装 Docker。

成功后，在群晖上执行 `docker login zot.zzbazinga.site:8888`，再用 `NAS_PANEL_IMAGE=zot.zzbazinga.site:8888/nas-panel:latest` 配合 `compose.image.yaml` 拉取并部署镜像。

看板读取：

- 主机 CPU 与内存：宿主机 `/proc`。
- 存储卷：宿主机挂载表和只读宿主机根目录，展示 ext4、Btrfs、XFS、NTFS、exFAT、VFAT 文件系统。
- 网络流量：宿主机 `/proc/net/dev`，约每秒计算并展示各网卡的收发速率，忽略 loopback 和常见容器虚拟接口。
- Docker 容器：Docker Engine API，包括 CPU、内存、网络 I/O 和磁盘 I/O。

Docker 容器统计通过 Docker Engine 的持续 Stats 流逐条推送到看板，容器增删状态每 5 秒同步一次；主机 CPU、内存和网络约每秒采样，存储容量随主机采样刷新。主机概览、存储、网络和容器资源均可通过卡片标题折叠/展开；容器资源列表默认收起。

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
