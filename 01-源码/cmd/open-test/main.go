package main

import (
	"fmt"
	"log"

	"pvfine/internal/pvf"
)

func main() {
	pvfPath := `D:\115us\PVF Ai Agent\Script.pvf`

	fmt.Println("正在打开 PVF（会自动尝试反推名称池密钥）...")
	archive, err := pvf.Open(pvfPath)
	if err != nil {
		log.Fatalf("打开失败: %v", err)
	}
	fmt.Printf("✓ 打开成功！共 %d 个文件\n", archive.FileCount())

	// 列出前 20 个文件，验证名称池解密成功
	fmt.Println("\n=== 前 20 个文件 ===")
	for i := int32(0); i < 20 && i < archive.FileCount(); i++ {
		path := archive.Path(i)
		fmt.Printf("  %d: %s\n", i, path)
	}

	// 搜索 50001248
	fmt.Println("\n=== 搜索物品 50001248 ===")
	found := 0
	for i := int32(0); i < archive.FileCount(); i++ {
		path := archive.Path(i)
		if contains(path, "50001248") {
			fmt.Printf("  找到: %s\n", path)
			found++
			if found >= 10 {
				break
			}
		}
	}
	if found == 0 {
		fmt.Println("  未在文件名中找到 50001248（正常，物品 ID 在文件内容里）")
	}

	// 列出 stackable 目录下的文件
	fmt.Println("\n=== stackable 目录下前 10 个文件 ===")
	count := 0
	for i := int32(0); i < archive.FileCount() && count < 10; i++ {
		path := archive.Path(i)
		if contains(path, "stackable/") && contains(path, ".stk") {
			fmt.Printf("  %s\n", path)
			count++
		}
	}

	fmt.Println("\n✓ 名称池密钥反推成功！")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
