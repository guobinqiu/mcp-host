# MCP Host [第三课]

[第二课](https://github.com/guobinqiu/mcp-demo-golang)

<img width="488" alt="image" src="https://github.com/user-attachments/assets/aee62cee-adc1-47eb-a594-bd6b8bc812b4" />

## 配置 MCP Servers

config.json

```
{
  "mcpServers": {
    "calculator": {
      "type": "stdio",
      "command": "bin/calculator-server",
      "args": []
    },
    "ip-location-query": {
      "type": "http",
      "command": "http://localhost:8080/mcp",
      "args": []
    }
  }
}
```

## 运行 MCP Servers

calculator 

```
go build -o bin/calculator-server tools/calculator/main.go
```

ip-location-query

```
go run tools/ip_location_query/main.go
```

## 运行 MCP Host

可见两个 MCP Servers 都被正常调用到了

```
go run main.go (in another terminal)

Initializing client...
[calculator] Connected to server: calculator-server 1.0.0
Initializing client...
[ip-location-query] Connected to server: ip-location-server 1.0.0
Type your queries or 'quit' to exit.
User: 查看183.193.158.228所在 地以及计算5+6等于几
Assistant: IP地址 **183.193.158.228** 位于中国上海市，具体信息如下：  
- **国家/地区**：中国 (CN)  
- **城市**：上海  
- **经纬度**：北纬31.2222°，东经121.4581°  
- **运营商**：中国移动通信集团上海有限公司 (AS24400)  

计算结果：  
**5 + 6 = 11**

User: 你调用了哪些工具
Assistant: 为了回答你的问题，我调用了以下两个工具：  

1. **IP 地址查询工具**：用于查询 `183.193.158.228` 的地理位置信息。  
2. **计算工具**：用于计算 `5 + 6` 的结果。  

如果你有其他问题，随时告诉我！

User: 告诉我工具名
Assistant: 我调用了以下两个工具来完成你的请求：  

1. **`ip_location_query`**（IP 地址查询工具）：用于查询 `183.193.158.228` 的地理位置信息。  
2. **`calculate`**（计算工具）：用于执行加法运算 `5 + 6`。  

如果你需要进一步了解这些工具的功能或使用方法，可以随时告诉我！
```
