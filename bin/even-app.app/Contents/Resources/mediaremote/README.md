MediaRemote Adapter v0.7.7，源码提交 `e3ff5021eb0875858bd05f48d2e9ba2e962d1cf6`。
来源 https://github.com/ungive/mediaremote-adapter/tree/v0.7.7，许可证见 LICENSE。

从该版本源码构建框架（x86_64 + arm64）：

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DCMAKE_OSX_DEPLOYMENT_TARGET=12.0
cmake --build build --target MediaRemoteAdapter
```

更新时完整复制 `build/MediaRemoteAdapter.framework`（保留符号链接）和
`bin/mediaremote-adapter.pl`。
