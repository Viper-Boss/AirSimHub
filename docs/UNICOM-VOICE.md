# UFI003 联通语音适配

2026-10-11：在 Debian 13 / 6.12.49-msm8916、设备自有 `UFI003_CT20211210` 基带和原 `Gen_3GPP` 配置上，上海联通 USIM 的 home operator code 为 46009。原 NAS voice domain preference 为 `CS_PREFERRED` (2)，IMS voice 不可用。仅将运行时偏好改为 `PS_PREFERRED` (3) 后，IMS 注册且 voice service 可用，ModemManager 拨打 10010 观察到 active。安装启动服务后，整机重启仍自动恢复注册与 voice service。

同日通过真实 NAS API 连续两次拨打 10010，均进入 active、拥有正确通话控制权且媒体可用。NAS 主动挂断约 0.83 秒返回，基带侧结束后 NAS 约 1.59 秒清除通话和占用。另一次通过配对认证媒体连接收到 276 帧下行 PCM，峰值 15697。测试没有按键、没有发送麦克风语音，也未验证实际浏览器播放、双向音质、联通短信收发或所有地区的联通卡；这些仍需现场验收。

辅助程序使用已运行的 `qmi-proxy`，分配并释放自己的 NAS client，严格匹配响应和结果。仅修改 NAS Set System Selection Preference 的 voice-domain TLV，change duration 为 POWER_CYCLE。不改选网、频段、APN、PDC/MBN、基带、NV、IMEI 或 SIM 文件。

## 构建及安装

```sh
cd node/agent
GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=false -o nassimhub-voice-domain ./cmd/nassimhub-voice-domain
# 把构建产物及 node/deploy/carrier/ 文件送到已验收的设备，再执行：
sudo sh install-carrier-voice.sh ./nassimhub-voice-domain
```

安装只适用于设备树兼容串 `thwc,ufi003`、aarch64 且已有 `/etc/nassimhub/local-volte.enabled` 的设备；安装时必须没有 Call 对象。需要 ModemManager、qmi-proxy、mmcli、systemd。该辅助程序独立于 Agent，无须替换出厂 Agent。

服务每 15 秒检查 SIM 的本籍运营商码。仅对已观察到的 46001、46009 请求 PS 优先；46001 的另一张联通 SIM 尚需同样的实际音频验收。未知、读失败、其他硬件、未验收设备及存在 Call 对象时不写设置。当前基带值不是 2/3 时不猜测修改。切换其他运营商时，仅在该服务实际把 2 改为 3 后才恢复 2；不接管用户原有的 PS 偏好。SIM 信息未就绪时保持现状。

NAS 是否允许外呼与 IMS 是否可用是两件事。换卡后的观察期应显示明确说明；授权管理员通过 NAS 安全中心处理信任限制。不得靠把 IMS 或语音能力字段写死来绕过。

## 回退

先结束通话；停止并禁用 `nassimhub-carrier-voice.service`。如 `/run/nassimhub-carrier-voice/ps-applied` 表明该服务曾应用偏好，可用辅助程序 `cs-preferred` 恢复原运行时值。完全断电也会重置 POWER_CYCLE 设置。不要在通话过程中切换设置。

当前是可单独安装的适配组件，尚未纳入暂停中的量产镜像构建与批量刷机流程。不是所有联通卡、地区或其他基带都已经通过验收。
