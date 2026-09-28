package ai

// Provider 是一个模型服务商预设：选它即自动带出接入地址与默认模型，用户仍可全部
// 手改（方案见 AI镶嵌.md §四.2，参考 opencode 的 provider 注册表思路——不锁死厂商，
// 一切以「OpenAI 兼容地址 + 模型名 + key」为准）。
type Provider struct {
	Name         string // 配置里存的标识（AppSettings.AI.Provider）
	Label        string // 界面显示名
	BaseURL      string // OpenAI 兼容 endpoint
	DefaultModel string // 默认模型；ollama/custom 留空由用户填
}

// Providers 内置预设表。顺序即设置界面的下拉顺序（自定义排第一）。
var Providers = []Provider{
	{Name: "custom", Label: "自定义（OpenAI 兼容地址）", BaseURL: "", DefaultModel: ""},
	{Name: "deepseek", Label: "DeepSeek", BaseURL: "https://api.deepseek.com/v1", DefaultModel: "deepseek-chat"},
	{Name: "qwen", Label: "通义千问", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", DefaultModel: "qwen-plus"},
	{Name: "kimi", Label: "Kimi", BaseURL: "https://api.moonshot.cn/v1", DefaultModel: "moonshot-v1-8k"},
	{Name: "openai", Label: "OpenAI", BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-4o-mini"},
	{Name: "ollama", Label: "Ollama（本机）", BaseURL: "http://localhost:11434/v1", DefaultModel: ""},
}

// LookupProvider 按配置名找预设。
func LookupProvider(name string) (Provider, bool) {
	for _, provider := range Providers {
		if provider.Name == name {
			return provider, true
		}
	}
	return Provider{}, false
}
