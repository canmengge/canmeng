package main

import (
	"fmt"
	"log"
	"os"

	"pvfine/internal/pvf"
)

func main() {
	pvfPath := `D:\115us\PVF Ai Agent\Script.pvf`
	outPath := `D:\115us\PVF Ai Agent\Script_std.pvf`

	fmt.Println("正在打开 Paged110 格式 PVF...")
	archive, err := pvf.Open(pvfPath)
	if err != nil {
		log.Fatalf("打开失败: %v", err)
	}
	fmt.Printf("打开成功！共 %d 个文件\n", archive.FileCount())

	fmt.Println("\n正在导出为标准 nkpi 格式...")
	fmt.Printf("输出文件: %s\n", outPath)

	// 直接写入标准格式
	// 我们需要绕过 Paged110 保存逻辑，强制重建为标准格式
	// 方法：调用 rebuild() 然后写入
	out, err := rebuildStandard(archive)
	if err != nil {
		log.Fatalf("重建失败: %v", err)
	}

	err = os.WriteFile(outPath, out, 0644)
	if err != nil {
		log.Fatalf("写入失败: %v", err)
	}

	fmt.Printf("\n✓ 导出成功！\n")
	fmt.Printf("  标准格式 PVF: %s\n", outPath)
	fmt.Printf("  文件大小: %d 字节 (%.1f MB)\n", len(out), float64(len(out))/1024/1024)
}

// rebuildStandard 强制重建为标准格式
// 这是一个简化版，直接调用内部的 rebuild 逻辑
func rebuildStandard(a *pvf.Archive) ([]byte, error) {
	// 我们用 SaveAs 但需要绕过 paged110 检查
	// 先尝试直接保存
	err := a.SaveAs(`D:\115us\PVF Ai Agent\Script_std_paged.pvf`)
	if err != nil {
		return nil, fmt.Errorf("保存 Paged110 失败: %w", err)
	}
	fmt.Println("  已保存 Paged110 格式（备用）")

	// 读取原始数据，手动重建标准格式
	// 实际上，我们只需要验证解密是否成功即可
	// 先列出前几个文件，验证名称池解密成功
	fmt.Println("\n  验证名称池解密...")
	count := 0
	total := int(a.FileCount())
	for i := 0; i < total && count < 10; i++ {
		path := a.Path(int32(i))
		if path != "" {
			fmt.Printf("    文件 %d: %s\n", i, path)
			count++
		}
	}

	// 如果名称池解密成功，说明我们可以继续
	if count == 0 {
		return nil, fmt.Errorf("名称池解密失败，无法获取文件名")
	}

	fmt.Println("\n  名称池解密成功！")

	// 返回原始数据（已经解密的逻辑数据）
	// 实际上我们需要的是重建为标准格式
	// 这里先返回一个标记
	return nil, fmt.Errorf("需要实现标准格式导出逻辑")
}
