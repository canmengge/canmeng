package services

import "testing"

// 体检只读跑通：给出文件数、目录数、扩展名分布，并成功加载字符串表清单。
func TestDoctorRunReportsArchiveShape(t *testing.T) {
	archivePath := writeSearchFixture(t, "doctor.pvf")
	c := NewCore()
	service := NewArchiveService(c)
	if _, err := service.Open(archivePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)

	report, err := NewDoctorService(c).Run()
	if err != nil {
		t.Fatal(err)
	}
	if report.FileCount != service.Info().FileCount {
		t.Fatalf("文件数不一致: %d vs %d", report.FileCount, service.Info().FileCount)
	}
	if report.FileCount == 0 {
		t.Fatal("文件数为 0，遍历没有生效")
	}
	if report.DirCount == 0 {
		t.Fatal("目录数为 0，目录统计没有生效")
	}
	if len(report.Extensions) == 0 {
		t.Fatal("扩展名分布为空")
	}
	// 清单来自内置 config/protected-string-tables.json：解析成功即说明数据可用。
	if report.TableSummary.Total == 0 {
		t.Fatal("字符串表清单未加载")
	}
	if report.Path == "" {
		t.Fatal("报告缺少归档路径")
	}
}

// 体检不会写入任何内容：跑完归档仍处于未修改状态。
func TestDoctorRunKeepsArchiveUnmodified(t *testing.T) {
	archivePath := writeSearchFixture(t, "doctor-readonly.pvf")
	c := NewCore()
	service := NewArchiveService(c)
	if _, err := service.Open(archivePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.closeArchive)

	if _, err := NewDoctorService(c).Run(); err != nil {
		t.Fatal(err)
	}
	if service.Info().ModifiedCount != 0 {
		t.Fatalf("体检后被标记为已修改: %d", service.Info().ModifiedCount)
	}
}
