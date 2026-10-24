# 用户与 SSH 管理

独立棒子后台的「系统管理」可以创建普通 Linux 用户，设置用户/root 密码，替换 Ed25519 公钥，授予或撤销 sudo，锁定/解锁普通账户，以及删除账户（保留家目录）。网页管理密码与 Linux 密码彼此独立；每次修改都需再次输入网页管理密码。服务账户不能由网页修改。

SSH 默认关闭，默认端口 2222；开关、端口和登录方式会保持到重启后。普通用户可以使用密码或公钥，root 只能使用公钥登录。公开固件没有默认密码、默认公钥或共享 SSH 主机密钥，主机密钥首次启用时在设备生成。页面显示实际服务运行状态和主机指纹。SSH 配置校验或服务启动失败会恢复先前配置；不要将未知错误当作成功。

安装依赖为 passwd、openssh-server、openssh-client、sudo；然后运行 `sh scripts/build-system-admin.sh`，将产物与 `node/deploy` 中的单元文件复制到设备，执行 `sh install-system-admin.sh HELPER_BINARY DEPLOY_DIRECTORY`。独立后台还需使用含本功能的 Agent。安装不打开用户 SSH，不设置任何 Linux 密码，不修改现有私有维护 SSH。

配置、公钥、主机密钥位于 `/etc/nassimhub/system-admin/`；账户由 Linux 的 passwd/shadow/group 管理，家目录位于 `/home`。普通重启会保持。完整 rootfs 重刷不会自动保留这些内容，需要另行备份和迁移；目前不能声称系统镜像升级已支持无损保留。用户自行运行的程序建议建立 systemd 服务，避免依赖 SSH 会话。

Agent 不以 root 运行，也不接受任意 shell 命令。独立 root 服务仅接受固定操作，使用 Unix 套接字和 SO_PEERCRED 校验 Agent 用户。密码只通过子进程标准输入交给 chpasswd，不写入日志、命令行或状态接口。sudo 权限等同于允许用户管理整个系统，请只授予可信账户。

批量刷机包当前暂停；本功能的源码和安装工具不等于新的量产镜像已经构建和验收。

安装时将二进制暂存于 `/var/tmp` 或根分区，避免挤占 `/run` 内存盘；systemd 重新加载配置需要足够的 `/run` 可用空间。账户仍有正在运行的进程时 Linux 会拒绝删除，先停止该账户的会话和程序，再手动提交。

## 第三方程序管理

「系统管理」接口新增固定操作 `program_install`、`program_remove` 和 `program_status`（经由同一 Unix 套接字与 SO_PEERCRED 校验，不接受任意命令或单元文本）。一个"程序"是设备所有者放在 `/usr/local/lib/nassimhub-programs/<名称>/` 下的可执行文件：安装时管理器为其创建锁定的系统用户 `nassimhub-p-<名称>`（nologin、无家目录内容），生成 `nassimhub-program-<名称>.service` 并 `enable --now`。

资源上限由请求给出、服务端钳制：`memory_max_mib` 16–256（默认 64，`MemoryMax` 且 `MemorySwapMax=0`）、`cpu_weight` 1–100（默认 20）、`tasks_max` 16–256（默认 64）。单元内容完全由管理器生成，请求不能夹带任何单元指令；沙箱为严格档：`ProtectSystem=strict`、`PrivateTmp`、`PrivateDevices`、`NoNewPrivileges`、空 `CapabilityBoundingSet`、`SystemCallFilter=@system-service`、`MemoryDenyWriteExecute` 等，程序数据目录 `/var/lib/nassimhub-programs/<名称>`（0750，属程序用户）是唯一可写路径。需要更多权限的程序不应通过本管理器安装。崩溃循环的程序由 `StartLimitBurst=5`/300 秒止损，任何程序故障都不会重启整机。

卸载会停止并删除单元、删除程序数据目录和用户，属预期数据清除。可执行文件必须位于自身程序目录内、root 所有、非组/其他可写，否则拒绝安装；安装失败不留半装单元。日志走 journald（`SyslogIdentifier=nassimhub-program-<名称>`），受日志总量上限保护。

**边界**：仅离线测试（含 root 下完整安装/卸载流程测试），未在真机验证；网页界面尚未接入这些操作；379 MiB 内存设备上所有程序的 `MemoryMax` 总和没有全局调度，所有者需自行留出通话与管理余量；本节条目在 `docs/KNOWN_ISSUES.md` 有对应声明。
