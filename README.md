# Syno Panel

一个面向群晖 NAS 的轻量级 Docker 资源看板。使用 Go 标准库构建，无需账号登录或额外服务；浏览器通过 SSE 接收服务端推送，Docker 容器资源直接使用 Docker Stats 持续流，主机 CPU、内存和网络指标约每秒采样，界面适配手机和桌面屏幕。

## 部署

在已安装 Container Manager（或 Docker）的群晖上，将本项目放到 NAS 后，在项目目录执行：

```sh
docker compose up -d --build
```

然后在浏览器访问 `http://群晖IP:5666`。也可以在 Container Manager 中通过 `compose.yaml` 创建项目。

## 使用已构建的 x86_64 镜像

如果使用预先构建的镜像包，将 `syno-panel-amd64.tar` 上传到群晖并导入 Container Manager，或通过 SSH 执行：

```sh
docker load -i syno-panel-amd64.tar
docker compose -f compose.image.yaml up -d
```

`compose.image.yaml` 默认使用已导入的 `syno-panel:latest`，不会在群晖上重新编译。若使用 GHCR 上的镜像，直接拉取并部署：

```sh
SYNO_PANEL_IMAGE=ghcr.io/zxkbazinga/syno-panel:latest docker compose -f compose.image.yaml pull
SYNO_PANEL_IMAGE=ghcr.io/zxkbazinga/syno-panel:latest docker compose -f compose.image.yaml up -d
```

镜像为公开包时无需登录即可拉取。若将包设为私有，需先在群晖上用具备 `read:packages` 权限的 Personal Access Token 登录：`docker login ghcr.io -u zxkbazinga --password-stdin`，不要把令牌写入 Compose 文件。

## 使用 GitHub Actions 构建并推送到 GHCR

仓库中的 `.github/workflows/publish-image.yaml` 会在推送到 `main` 分支或手动触发时，使用 GitHub 托管的 `ubuntu-latest` runner 构建群晖 x86_64 使用的 `linux/amd64` 镜像并推送到 GHCR（`ghcr.io/zxkbazinga/syno-panel`）。每次构建都会更新 `latest` 并发布以提交 SHA 命名的不可变镜像标签；符合 Conventional Commits 的版本更新还会发布 SemVer 镜像标签并创建对应的 Git 标签（格式 `v主版本.次版本.补丁版本`）。页面底部会显示镜像构建时注入的版本号。

镜像包含 OCI `org.opencontainers.image.vendor` 元数据，仓库界面可据此显示发布者 `zzbazinga`。更新 Dockerfile 后需重新构建并推送镜像，已有镜像标签不会自动补上该元数据。

版本升级规则（统计自上一个 `vX.Y.Z` Git 标签之后的所有提交）：

- `feat:`：次版本号 +1，补丁号归零。
- `fix:` 或 `perf:`：补丁号 +1。
- `BREAKING CHANGE:` 正文，或 `feat!:`、`fix!:`、`perf!:`：主版本号 +1，次版本号和补丁号归零。
- 多种提交同时存在时，按主版本、次版本、补丁版本的优先顺序取最高升级级别。其他提交类型只更新 `latest` 和 SHA 标签，不创建新 SemVer 版本。
- 第一次运行且仓库没有 `vX.Y.Z` 标签时，从 `v0.1.0` 开始。

例如推送 `feat: add disk alerts` 后发布 `v0.2.0` 和 `latest`；推送 `fix: correct memory usage` 后发布 `v0.2.1`。部署时可以固定镜像版本（如 `ghcr.io/zxkbazinga/syno-panel:v0.2.1`），或使用持续更新的 `ghcr.io/zxkbazinga/syno-panel:latest`。

工作流使用内置的 `GITHUB_TOKEN` 登录 GHCR，无需额外配置 Secrets。仓库需开启 **Settings → Actions → General → Workflow permissions** 中的 `Read and write permissions`（工作流已声明 `contents: write` 和 `packages: write`：前者用于把 Git 标签推回仓库，后者用于推送镜像）。`ubuntu-latest` 已预装 Git 和 Docker，无需自托管 runner。

容器镜像包在**用户账户下**而非仓库内：点右上角头像 → **Your profile → Packages**，或直接访问 `https://github.com/zxkbazinga/syno-panel/pkgs/container/syno-panel`。包的可见性默认为私有，若需公开匿名拉取，进入包页面 **Package settings → Change visibility → Public**。

成功后，在群晖上用 `SYNO_PANEL_IMAGE=ghcr.io/zxkbazinga/syno-panel:latest` 配合 `compose.image.yaml` 拉取并部署镜像。

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
