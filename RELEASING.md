# 发布流程

`发布检查` workflow 只验证模块并生成源码候选包，不创建 Git tag 或 GitHub
Release。正式发布前必须确认版本、契约 tag 和 Go module 路径，再通过独立审核的
发布变更创建 `v*` tag。
