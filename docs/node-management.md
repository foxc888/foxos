# 节点导入说明

FoxOS 节点导入以 ClashManager 的批量导入思路为基础，导入过程先解析和校验全部链接，再写入数据库。

## 支持格式

- Shadowsocks：`ss://`
- VMess：`vmess://`
- VLESS：`vless://`
- Trojan：`trojan://`
- Hysteria2：`hysteria2://` 或 `hy2://`
- SOCKS5：`socks5://`
- HTTP/HTTPS：`http://` 或 `https://`

## 导入流程

1. 在内存中解析全部链接。
2. 校验服务器、端口和协议必填字段。
3. 检查重复名称与重复节点。
4. 展示导入预览，密码和 UUID 始终脱敏。
5. 用户确认后事务写入 SQLite。
6. 生成临时 Mihomo 配置并验证。
7. 验证成功后才允许应用。

如果批次中任意链接无法解析，默认整批不写入，防止只导入一半造成配置不一致。
