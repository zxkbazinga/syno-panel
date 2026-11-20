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

## 本地手动打包镜像

不依赖 GitHub Actions，直接在本地构建并导出可导入群晖的镜像包。群晖 x86_64 机型需要 `linux/amd64` 镜像，按本机架构选择命令。

**本机是 Apple Silicon（arm64，如 M 系列 Mac）** —— 必须跨平台构建，否则导出的镜像在群晖上无法运行：

```sh
docker buildx build --platform linux/amd64 \
  --build-arg APP_VERSION=v0.1.8 \
  --load -t syno-panel:latest .
docker save syno-panel:latest -o syno-panel-amd64.tar
```

**本机是 x86_64** —— 直接构建即可：

```sh
docker build --build-arg APP_VERSION=v0.1.8 -t syno-panel:latest .
docker save syno-panel:latest -o syno-panel-amd64.tar
```

`APP_VERSION` 只影响页面底部显示的版本号，可填任意值（如 `local`）。导出的 `syno-panel-amd64.tar` 上传到群晖后，按上一节的 `docker load -i` 方式导入。若要把源码打包发给别人自行构建：

```sh
tar -czf syno-panel-deploy.tar.gz \
  go.mod main.go main_test.go LICENSE Dockerfile \
  compose.yaml compose.image.yaml .dockerignore README.md web .github
```

两个打包产物（`syno-panel-amd64.tar`、`syno-panel-deploy.tar.gz`）已在 `.gitignore` 中，不会误提交。

## 使用 GitHub Actions 构建并推送到 GHCR

仓库中的 `.github/workflows/publish-image.yaml` 会在推送到 `main` 分支或手动触发时，使用 GitHub 托管的 `ubuntu-latest` runner 构建群晖 x86_64 使用的 `linux/amd64` 镜像并推送到 GHCR（`ghcr.io/zxkbazinga/syno-panel`）。每次构建都会发布一个 SemVer 版本标签（格式 `v主版本.次版本.补丁版本`）并同步更新 `latest`，同时创建对应的 Git 标签。页面底部会显示镜像构建时注入的版本号。推送到其他分支不会触发构建。

镜像包含 OCI `org.opencontainers.image.vendor` 元数据，仓库界面可据此显示发布者 `zzbazinga`。更新 Dockerfile 后需重新构建并推送镜像，已有镜像标签不会自动补上该元数据。

版本升级规则（统计自上一个 `vX.Y.Z` Git 标签之后的所有提交，**每次构建都会发版**）：

- `feat:`：次版本号 +1，补丁号归零。
- `BREAKING CHANGE:` 正文，或 `feat!:`、`fix!:`、`perf!:`：主版本号 +1，次版本号和补丁号归零。
- 其他所有提交（含 `fix:`、`perf:`、`ci:`、`docs:`、`refactor:` 等）：补丁号 +1。
- 多种提交同时存在时，按主版本、次版本、补丁版本的优先顺序取最高升级级别。
- 第一次运行且仓库没有 `vX.Y.Z` 标签时，从 `v0.1.0` 开始。

例如最新为 `v0.1.8` 时，推送 `fix:` 或 `ci:` 等提交会发布 `v0.1.9`，推送 `feat: add disk alerts` 会发布 `v0.2.0`。部署时可以固定镜像版本（如 `ghcr.io/zxkbazinga/syno-panel:v0.1.8`），或使用持续更新的 `ghcr.io/zxkbazinga/syno-panel:latest`。

工作流使用内置的 `GITHUB_TOKEN` 登录 GHCR，无需额外配置 Secrets。仓库需开启 **Settings → Actions → General → Workflow permissions** 中的 `Read and write permissions`（工作流已声明 `contents: write` 和 `packages: write`：前者用于把 Git 标签推回仓库，后者用于推送镜像）。`ubuntu-latest` 已预装 Git 和 Docker，无需自托管 runner。

容器镜像包在**用户账户下**而非仓库内：点右上角头像 → **Your profile → Packages**，或直接访问 `https://github.com/zxkbazinga/syno-panel/pkgs/container/syno-panel`。包的可见性默认为私有，若需公开匿名拉取，进入包页面 **Package settings → Change visibility → Public**。

成功后，在群晖上用 `SYNO_PANEL_IMAGE=ghcr.io/zxkbazinga/syno-panel:latest` 配合 `compose.image.yaml` 拉取并部署镜像。

## 安全提示

本看板按需求不提供登录。请仅在可信的家庭/局域网中使用，不要将端口 5666 直接暴露到公网；如需远程访问，请通过 VPN 或具备身份验证的反向代理。

虽然 Socket 文件以只读方式挂载，Docker Socket API 本身仍允许访问 Docker Engine 的管理接口。请只运行可信的看板镜像，并限制其网络可达范围。宿主机根目录仅以只读方式挂载，用于获取各存储卷的容量信息。

## 开发与验证

```sh
go test ./...
go run .
```

本地运行时，主机指标会读取当前系统的 `/proc`；Docker 数据需要当前用户可访问 `/var/run/docker.sock`。

## 分支开发与 main 保护

`main` 受保护，改动通过 Pull Request 合入；镜像只在合并到 `main` 时构建推送，推送到其他分支不会触发。日常开发流程：

```sh
git switch main
git pull origin main
git switch -c feat/xxx          # 按改动类型命名，如 feat/ fix/ docs/

# 开发、测试
go test ./...

git add -A
git commit -m "feat: 新增某功能"
git push -u origin feat/xxx
```

随后在 GitHub 网页发起 Pull Request，目标分支选 `main`，评审通过后合并。合并产生的 `main` push 会触发构建并按提交类型自动发版。如果需要长期开发分支，可用 `git switch -c develop main && git push -u origin develop` 创建（同样不会触发构建）。

### 开启分支保护（仓库管理员在网页操作）

进入仓库 **Settings → Branches → Branch protection rules → Add branch protection rule**：

1. Branch name pattern 填 `main`。
2. 勾选 **Require a pull request before merging**，建议同时勾选 **Require approvals**（至少 1 人）。
3. 按需勾选 **Require status checks to pass before merging**（若后续添加 CI 检查）。
4. 勾选 **Do not allow bypassing the above settings**，必要时勾选 **Include administrators**。
5. 取消勾选 **Allow force pushes** 与 **Allow deletions**。
6. 点击 Create 保存。

开启后直接 `git push origin main` 会被拒绝，只能通过 PR 合并。工作流用 `GITHUB_TOKEN` 推送的是 Git tag 而非分支，不受分支保护影响，发版照常工作。
