package main

import (
	"fmt"
	"log"
	"strings"

	"pvfine/internal/pvf"
)

func main() {
	pvfPath := `D:\115us\PVF Ai Agent\Script.pvf`
	outPath := `D:\115us\PVF Ai Agent\Script_modified.pvf`

	fmt.Println("正在打开 PVF...")
	archive, err := pvf.Open(pvfPath)
	if err != nil {
		log.Fatalf("打开失败: %v", err)
	}
	fmt.Printf("打开成功！共 %d 个文件\n\n", archive.FileCount())

	// 读取物品 50001248
	itemPath := "stackable/dfo/event/2015/1006/priestpremium/50001248.stk"
	fmt.Printf("正在读取: %s\n", itemPath)

	idx, found := archive.Find(itemPath)
	if !found {
		log.Fatalf("未找到文件: %s", itemPath)
	}

	oldText, err := archive.Text(idx)
	if err != nil {
		log.Fatalf("读取文件失败: %v", err)
	}

	// 找到 [exp bonus rate] 字段并修改
	fmt.Println("\n=== 修改前 ===")
	lines := strings.Split(oldText, "\n")
	for i, line := range lines {
		if strings.Contains(line, "exp bonus rate") {
			fmt.Printf("  行 %d: %s\n", i, line)
			if i+1 < len(lines) {
				fmt.Printf("  行 %d: %s\n", i+1, lines[i+1])
			}
		}
	}

	// 修改经验倍率为 5
	newText := strings.Replace(oldText, "[exp bonus rate]\n\t2", "[exp bonus rate]\n\t5", 1)
	if newText == oldText {
		log.Fatal("未找到 [exp bonus rate] 2 字段")
	}

	fmt.Println("\n=== 修改后 ===")
	lines = strings.Split(newText, "\n")
	for i, line := range lines {
		if strings.Contains(line, "exp bonus rate") {
			fmt.Printf("  行 %d: %s\n", i, line)
			if i+1 < len(lines) {
				fmt.Printf("  行 %d: %s\n", i+1, lines[i+1])
			}
		}
	}

	// 应用修改
	fmt.Println("\n正在应用修改...")
	err = archive.SetText(idx, newText)
	if err != nil {
		log.Fatalf("应用修改失败: %v", err)
	}

	// 保存为新的 PVF
	fmt.Printf("正在保存到: %s\n", outPath)
	err = archive.SaveAs(outPath)
	if err != nil {
		log.Fatalf("保存失败: %v", err)
	}

	fmt.Println("\n✓ 修改完成！")
	fmt.Printf("  原文件: %s\n", pvfPath)
	fmt.Printf("  新文件: %s\n", outPath)
	fmt.Printf("  修改内容: 物品 50001248 经验倍率 2 → 5（五倍经验）\n")
}
