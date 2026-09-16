# even-g2-go v0.2 接入清单

本文记录桌面端对 `even-g2-go v0.2.0-rc.1` 的接入范围和阶段，避免把 SDK 新能力与已完成的产品功能混在一起。

## 已接入

- 单设备扫描连接：`g2.Connect`
- 自动重连：`AutoReconnect: true`、`ReconnectDelay: time.Second`
- 临时断开与恢复：`Client.Disconnect`、`Client.Reconnect`
- 应用退出永久释放：`Client.Close`
- 连接状态快照：`Client.Status`
- 提词器、原生文本、原生列表和图片
- 带状态图标的文本页面及图标增量更新
- 多设备错误识别：`g2.ErrMultipleDevices`

## 已完成的第二阶段：设备与状态

- 使用 `ScanArms` 展示左右镜腿候选项
- 使用 `NewDevice` 校验用户选择，使用 `ConnectDevice` 显式连接
- 将设备 ID、名称、左右状态、能力和设置状态暴露到前端
- 使用 `SubscribeStatus` 驱动状态更新，替代前端定时轮询设备连接状态

## 已完成的第三阶段：设置与控制

- `RequestDeviceSettings`
- `SetBrightness`
- `SetHeadUp`
- `SetScreenPosition`
- 设置读取失败、范围错误和设备未就绪提示

## 已完成的第四阶段：输入与音频

- 使用 `SubscribeEvents` 接收列表、文本和系统输入
- 使用 `SubscribeAudio` 接收独立 LC3 流
- `StartMicrophone`、`StopMicrophone`
- context 取消时关闭订阅，并在断开或退出时清理录音状态
- 前端展示最后一次输入事件、录音状态和帧统计。原始 LC3 数据暂不跨 Wails 高频传输。

## 已完成的第五阶段：Dashboard

- `ShowDashboard`
- 实验开关下开放 `ConfigureDashboard`
- 实验开关下开放 `PushDashboardSchedule` 和 `ClearDashboardSchedule`

Dashboard 排序和 Schedule 注入仍属于实验能力，必须由用户显式开启后才能调用。
