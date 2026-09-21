# 游戏数据加密配置

9.0.0 起使用独立的顶层配置，各区服分别填写十六进制 AES key 与 IV：

```yaml
crypto:
  jp: {key: "", iv: ""}
  en: {key: "", iv: ""}
  cn: {key: "", iv: ""}
  tw: {key: "", iv: ""}
  kr: {key: "", iv: ""}
```

部署时必须将所需区服的真实值填入对应条目。key 与 iv 必须成对配置；AES key 为 16/24/32 字节，IV 为 16 字节，以上长度均指十六进制解码后长度。空配置不能执行该区服上传的加解密。

旧 `sekai_client.en_server_aes_*`、`cn_server_aes_*`、`other_server_aes_*` 已移除，不提供兼容或共享区服回退。即便多个区服使用同一组参数，也应逐区服显式填写。未知区服和半组新配置在配置加载时拒绝；具体十六进制格式及长度在创建区服 cryptor 时验证。

启动装配和 `cmd/nuverse-restore-compare` 原始上传输入均使用此配置。运行时复制配置 map，不随调用者修改原 map 而改变。不要把真实部署配置或上传样本放进发布包；示例只有空值。
