package main

import (
	"fmt"
	"log"

	"pvfine/internal/pvf"
)

func main() {
	pvfPath := `D:\115us\PVF Ai Agent\Script.pvf`

	fmt.Println("正在打开 PVF...")
	archive, err := pvf.Open(pvfPath)
	if err != nil {
		log.Fatalf("打开失败: %v", err)
	}
	fmt.Printf("打开成功！共 %d 个文件\n\n", archive.FileCount())

	// 读取物品 50001248
	itemPath := "stackable/dfo/event/2015/1006/priestpremium/50001248.stk"
	fmt.Printf("正在读取: %s\n\n", itemPath)

	idx, found := archive.Find(itemPath)
	if !found {
		log.Fatalf("未找到文件: %s", itemPath)
	}

	text, err := archive.Text(idx)
	if err != nil {
		log.Fatalf("读取文件失败: %v", err)
	}

	fmt.Println("=== 文件内容 ===")
	fmt.Println(text)
	fmt.Println("=== 文件内容结束 ===")
	fmt.Printf("\n文件大小: %d 字节\n", len(text))
}
