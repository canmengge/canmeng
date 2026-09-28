package main

import (
	"fmt"
	"log"
	"strings"

	"pvfine/internal/pvf"
)

func main() {
	pvfPath := `D:\115us\PVF Ai Agent\Script.pvf`

	fmt.Println("正在打开 PVF 文件...")
	archive, err := pvf.Open(pvfPath)
	if err != nil {
		log.Fatalf("打开失败: %v", err)
	}
	fmt.Printf("打开成功！共 %d 个文件\n\n", archive.FileCount())

	// 读取 stackable.lst
	fmt.Println("正在读取 stackable/stackable.lst ...")
	lstIdx, found := archive.Find("stackable/stackable.lst")
	if !found {
		log.Fatal("未找到 stackable/stackable.lst")
	}
	lstText, err := archive.Text(lstIdx)
	if err != nil {
		log.Fatalf("读取 lst 失败: %v", err)
	}

	// 搜索 50001248
	targetID := "50001248"
	lines := strings.Split(lstText, "\n")
	fmt.Printf("lst 共 %d 行，正在搜索 %s ...\n\n", len(lines), targetID)

	for i, line := range lines {
		if strings.Contains(line, targetID) {
			fmt.Printf("找到匹配行 %d: %s\n", i+1, line)
		}
	}

	// 读取物品文件
	fmt.Println("\n正在查找物品文件...")
	// 尝试常见的 stackable 路径格式
	candidates := []string{
		fmt.Sprintf("stackable/etc/%s.stk", targetID),
		fmt.Sprintf("stackable/consumable/%s.stk", targetID),
		fmt.Sprintf("stackable/%s.stk", targetID),
	}

	for _, path := range candidates {
		if idx, ok := archive.Find(path); ok {
			fmt.Printf("找到物品文件: %s (索引 %d)\n", path, idx)
			text, err := archive.Text(idx)
			if err != nil {
				log.Fatalf("读取文件失败: %v", err)
			}
			fmt.Println("\n=== 文件内容 ===")
			fmt.Println(text)
			return
		}
	}

	// 如果没找到，列出 stackable 目录下的前 20 个文件
	fmt.Println("未在常见路径找到，列出 stackable 目录下的文件示例...")
	count := 0
	for i := int32(0); i < archive.FileCount(); i++ {
		path := archive.Path(i)
		if strings.HasPrefix(path, "stackable/") && strings.HasSuffix(path, ".stk") {
			fmt.Printf("  %s\n", path)
			count++
			if count >= 20 {
				break
			}
		}
	}
}
