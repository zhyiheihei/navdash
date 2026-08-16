# navdash

个人服务门户（`nav.zhyi.xin`）：Go 标准库单二进制 + 无构建 vanilla 前端。

## 结构

- `main.go`：全部后端逻辑（OIDC / session / 静态资源 / entries API），
  零第三方依赖（`go.mod` 无 require，`vendorHash = null`）。
- `web/`：`embed` 进二进制的静态资源。改前端无需任何构建步骤，
  `go build` 直接打包。

## 构建与测试

```bash
go build .        # 产出 ./navdash
go vet ./...      # 静态检查
go test ./...     # 无外部依赖，可离线跑
```

`CGO_ENABLED=0` 构建出纯静态二进制（Nix 包定义已设置）。

## 运行

```bash
NAVDASH_OIDC_ISSUER=https://login.example.com \
NAVDASH_OIDC_CLIENT_ID=navdash \
NAVDASH_OIDC_CLIENT_SECRET=<secret> \
NAVDASH_SESSION_KEY=<32+ 字节 hex> \
./navdash
```

- 默认监听 `127.0.0.1:13828`；NixOS 部署固定 `127.0.0.1:13833`
  （见 nixos-config `helpers/constants/ports.nix`、`nixos/optional-apps/navdash.nix`）。
- entries.json 可由 `NAVDASH_ENTRIES` 指定，或交互式输入；格式见 README。

## 约定

- 前端样式复刻 DeepSeek Harness 官网设计 token（配色/字体），不搬对方
  logo/文案；字体为 SIL OFL 可变字体，自托管于 `web/assets/fonts/`
  （含 OFL.txt）。
- 匿名仅下发 `accessibleBy == "public"` 的条目；登录后下发全部。
- 提交用 conventional commits，中文说明「为什么」。