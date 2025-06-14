package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/imroc/req/v3"
	"github.com/panjf2000/ants/v2"
	"github.com/tidwall/gjson"
)

type Config struct {
	Nocaptcha    string `json:"nocaptcha"`
	DynamicProxy string `json:"dynamicProxy"`
	Retry        int    `json:"retry"`
}

type Task struct {
	Wallet string
	Config Config
}

func redtask() []string {
	// 读取wallet.txt
	wallets, err := os.ReadFile("wallet.txt")
	if err != nil {
		fmt.Println("❌ 读取钱包文件错误:", err)
		return nil
	}

	return strings.Split(string(wallets), "\n")
}

// bypasscf 获取Cloudflare验证token
func bypasscf(config Config) (string, error) {
	client := req.C()
	client.SetProxyURL(config.DynamicProxy)
	// 忽略证书
	client.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true})

	headers := map[string]string{
		"User-Token": config.Nocaptcha,
	}

	postData := map[string]string{
		"href":    "https://irys.xyz/faucet",
		"sitekey": "0x4AAAAAAA6vnrvBCtS4FAl-",
	}

	// 设置重试次数
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		resp, err := client.R().
			SetHeaders(headers).
			SetFormData(postData).
			Post("http://api.nocaptcha.io/api/wanda/cloudflare/universal")

		if err != nil {
			fmt.Printf("🔄 获取Cloudflare token第%d次尝试失败: %v\n", i+1, err)
			if i < maxRetries-1 {
				time.Sleep(time.Second * 2) // 失败后等待2秒再重试
				continue
			}
			return "", fmt.Errorf("❌ 获取Cloudflare token失败: %v", err)
		}

		fmt.Printf("📡 获取Cloudflare token状态码: %d\n", resp.StatusCode)

		token := gjson.Get(resp.String(), "data.token").String()
		if token != "" {
			fmt.Println("✅ 成功获取Cloudflare token")
			return token, nil
		}

		fmt.Printf("⚠️ 第%d次尝试未获取到token\n", i+1)
		if i < maxRetries-1 {
			time.Sleep(time.Second * 2)
			continue
		}
	}

	return "", fmt.Errorf("❌ 达到最大重试次数，未能获取Cloudflare token")
}

func faucetTask(wallet string, config Config) error {
	client := req.C()
	client.SetProxyURL(config.DynamicProxy)
	// 忽略证书
	client.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true})

	// 设置重试次数
	maxRetries := config.Retry
	for i := 0; i < maxRetries; i++ {
		// 获取Cloudflare token
		token, err := bypasscf(config)
		if err != nil {
			fmt.Printf("[%s] 🔄 第%d次获取token失败: %v\n", wallet, i+1, err)
			if i < maxRetries-1 {
				time.Sleep(time.Second * 2)
				continue
			}
			return fmt.Errorf("❌ 获取Cloudflare token失败: %v", err)
		}

		postData := map[string]string{
			"captchaToken":  token,
			"walletAddress": wallet,
		}

		resp, err := client.R().
			SetBody(postData).
			Post("https://irys.xyz/api/faucet")

		if err != nil {
			fmt.Printf("[%s] 🔄 第%d次请求失败: %v\n", wallet, i+1, err)
			if i < maxRetries-1 {
				time.Sleep(time.Second * 2)
				continue
			}
			return fmt.Errorf("❌ 请求失败: %v", err)
		}

		// 检查状态码
		if resp.StatusCode != 200 {
			fmt.Printf("[%s] ⚠️ 第%d次请求状态码异常: %d\n", wallet, i+1, resp.StatusCode)
			if i < maxRetries-1 {
				time.Sleep(time.Second * 2)
				continue
			}
			return fmt.Errorf("❌ 请求状态码异常: %d", resp.StatusCode)
		}

		fmt.Printf("[%s] 📡 请求状态码: %d\n", wallet, resp.StatusCode)

		var result map[string]interface{}
		err = resp.UnmarshalJson(&result)
		if err != nil {
			fmt.Printf("[%s] ⚠️ 解析响应失败: %v\n", wallet, err)
			if i < maxRetries-1 {
				time.Sleep(time.Second * 2)
				continue
			}
			return fmt.Errorf("❌ 解析响应失败: %v", err)
		}

		// 检查响应内容
		if success, ok := result["success"].(bool); ok && success {
			fmt.Printf("[%s] ✅ 处理成功: %+v\n", wallet, result)
			return nil
		}

		// 处理验证码失败的情况
		if message, ok := result["message"].(string); ok {
			if strings.Contains(message, "Captcha verification failed") {
				fmt.Printf("[%s] ⚠️ 第%d次验证码验证失败，准备重试\n", wallet, i+1)
				if i < maxRetries-1 {
					time.Sleep(time.Second * 2)
					continue
				}
			}
		}

		fmt.Printf("[%s] ⚠️ 响应内容: %+v\n", wallet, result)
		if i < maxRetries-1 {
			time.Sleep(time.Second * 2)
			continue
		}
	}

	return fmt.Errorf("❌ 达到最大重试次数")
}

func processTask(task interface{}) {
	t := task.(Task)
	fmt.Printf("\n[%s] 🚀 开始处理\n", t.Wallet)
	err := faucetTask(t.Wallet, t.Config)
	if err != nil {
		fmt.Printf("[%s] ❌ 处理失败: %v\n", t.Wallet, err)
	}
}

func main() {
	// 解析config.json
	file, err := os.ReadFile("config.json")
	if err != nil {
		fmt.Println("❌ 读取配置文件错误:", err)
		return
	}

	var config Config
	err = json.Unmarshal(file, &config)
	if err != nil {
		fmt.Println("❌ 解析配置文件错误:", err)
		return
	}

	// 读取wallet.txt
	wallets := redtask()
	if len(wallets) == 0 {
		fmt.Println("❌ 没有找到钱包地址")
		return
	}

	fmt.Printf("📋 共找到 %d 个钱包地址\n", len(wallets))

	// 创建任务池
	p, err := ants.NewPool(5, ants.WithNonblocking(true))
	if err != nil {
		fmt.Println("❌ 创建线程池失败:", err)
		return
	}
	defer p.Release()

	// 等待所有任务完成
	var wg sync.WaitGroup

	// 提交任务
	for _, wallet := range wallets {
		wallet = strings.TrimSpace(wallet)
		if wallet == "" {
			continue
		}

		wg.Add(1)
		task := Task{
			Wallet: wallet,
			Config: config,
		}

		err := p.Submit(func() {
			defer wg.Done()
			processTask(task)
		})

		if err != nil {
			fmt.Printf("[%s] ❌ 提交任务失败: %v\n", wallet, err)
			wg.Done()
			continue
		}

		// 添加短暂延迟，避免请求过于密集
		time.Sleep(time.Millisecond * 500)
	}

	// 等待所有任务完成
	wg.Wait()
	fmt.Println("\n✨ 所有钱包处理完成")
}
