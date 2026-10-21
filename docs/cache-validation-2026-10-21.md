# 缓存修复验证记录（2026-10-21）

## 实际改动

会话累计发现工具类别并持久化；未发现类别的合法调用保留原参数进入正常执行检查；固定 Instructions，动态任务信息追加至模型上下文；委派额度不再改变工具声明；缓存提示跨任务保持稳定。新增请求指纹、身份、重试与本地变化诊断，TUI 区分会话累计缓存率与最近主 agent 请求明细。

全量命令测试同时发现并修复 Windows capability API 的 DLL、BOOL 返回值和 SID 指针数组声明错误，并增加实际系统调用回归测试。

## 真实 API 记录

使用当前 DeepSeek 配置、low effort、每请求最多 2,048 输出 token，在隔离目录分别运行 Windows 原生沙箱和 Linux Docker root 容器。两端合计严格限制为 24 次 API 请求，没有继续加预算重试。

| 环境 | 请求 | 输入 token | 命中 token | 输出 token | 全段命中率 | 无本地前缀变化时命中率 |
|---|---:|---:|---:|---:|---:|---:|
| Windows | 12 | 83,370 | 63,232 | 920 | 75.85% | 97.11% (8 次) |
| Linux | 12 | 79,714 | 70,784 | 837 | 88.80% | 96.82% (7 次) |

两端都实际完成写文件、命令授权与执行、再次编辑、跨轮读取、生成计划，以及计划实施后的文件变更；磁盘文件最终均为 `gamma`。没有不可见工具调用拒绝，也没有工具执行失败。新类别首次发现和 Plan 权限过滤仍改变工具声明。

**真实 API smoke 测试未完整通过：** 两端都在实施后的最终自然语言回复之前达到各自 12 次请求上限，测试按预算保护失败退出。工具结果已独立核实，但不能据此宣称最后回复与整套真实 API 流程通过。没有继续发起付费请求。

这些是短任务样本，没有修复前的同条件 API 对照；两端使用同一服务，可能复用服务端已经存在的缓存。不能据此承诺长会话或所有请求固定达到某个百分比。

## 验证入口与构建

真实 API 验证入口位于 luneCore：设置 `LUNE_CACHE_SMOKE_CONFIG` 后运行 `go test ./internal/runtime -run '^TestCacheRealAPISmoke$' -count=1 -v`；默认测试跳过该入口。可设置 `LUNE_CACHE_SMOKE_DIR` 保存临时会话与日志、`LUNE_CACHE_SMOKE_HELPER` 指定沙箱程序。单次最多 12 次调用，连续运行不得擅自扩大本次 24 次总预算。

自动测试覆盖类别累计和跨重启、原参数执行、四档权限、Plan 禁止写入、授权拒绝、上下文快照、压缩、协议序列化、委派限制、稳定缓存 key、原子保存及 TUI 诊断。Linux 的验证环境是独立 Docker 容器，并非用户提供旧日志的 wslc test 容器；Linux 桌面 GUI 未纳入 CLI/TUI 验收。

构建目标：Windows amd64、Linux amd64、Linux arm64 的主程序和 sandbox，全部使用 `-trimpath`。构建来自本地修改后的两个模块，尚未提交。


最终自动测试结果：

| 环境 | 检查 | 结果 |
|---|---|---|
| Windows | luneCore `go test ./...` | 通过 |
| Windows | luneAgent `go test ./...` | 通过 |
| Linux Docker | luneCore `go test ./...` | 通过 |
| Linux Docker | luneAgent `internal/app`、`internal/cli`、`internal/tui`、`internal/settings` | 通过 |

Windows 与 Linux amd64 构建产物均已实际运行 `--version`；三种主程序已检查构建信息包含 `-trimpath=true`。Linux arm64 已交叉构建，未在 arm64 机器运行。Linux 整仓库 GUI 包需要 GTK/WebKit 开发依赖，未计入上述 CLI/TUI 检查。
