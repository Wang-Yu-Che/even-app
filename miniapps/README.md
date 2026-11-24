# Miniapp 开发

每个眼镜 miniapp 放在 `miniapps/<id>/`，并实现 `miniapps.MiniApp`。主程序持有
唯一的 BLE 连接；miniapp 负责自己的菜单描述、生命周期和事件识别。

## 接入

1. 新建 `miniapps/<id>/app.go`，实现 `ID`、`MenuItem`、`Start`、
   `HandleEvent` 和 `Stop`。
2. 把应用构造器加入根目录 `miniapp_setup.go` 的 `installedMiniApps`。
3. 为菜单启动、输入事件和退出清理添加包内测试。
4. 运行 `go test ./...`。

注册顺序就是眼镜菜单顺序。`HandleEvent` 只应消费属于自己的菜单、容器或当前
页面事件；返回 `true` 后注册表不会再把同一事件交给后续应用。

当前是编译期插拔：删除清单中的一行即可移除应用，不使用 Go 动态插件。这样
资源可以继续通过 `go:embed` 打进跨平台应用，miniapp 仍可在自己的目录独立开发
和测试。
