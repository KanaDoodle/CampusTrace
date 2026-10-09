package rag

func WarningMessage(code string) string {
 switch code {
 case "RETRIEVAL_INDEX_PARTIAL": return "部分材料尚未建立语义索引，仍参加关键词检索。"
 case "RETRIEVAL_INDEX_REQUIRED": return "当前模型尚未建立材料索引，本次使用关键词检索。"
 case "RETRIEVAL_DIMENSION_CHANGED": return "检索模型的向量维度变化，请清除索引后重新建立；本次保留关键词结果。"
 case "RETRIEVAL_AUTH_FAILED": return "检索服务拒绝了密钥，本次使用可用结果，请核对检索模型设置。"
 case "RETRIEVAL_RATE_LIMIT": return "检索服务请求较多，本次使用可用结果，可稍后重试。"
 case "RETRIEVAL_BALANCE_LOW": return "检索服务余额不足，本次使用可用结果。"
 default: return "检索模型本次未完成请求，已保留可用的本地结果；可核对服务设置后重试。"
 }
}
