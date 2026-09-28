package objectview

import (
	"fmt"
	"os"
	"path/filepath"

	appconfig "pvfine/config"
)

const sourceRelativePath = "config/objectview.json"

// LoadDefault 用编译进二进制的内置规则构建目录。
func LoadDefault() (Catalog, error) {
	return Parse(appconfig.ObjectViewJSON)
}

// LoadFile 从磁盘加载并校验规则。
func LoadFile(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("读取对象视图规则失败: %w", err)
	}
	return Parse(data)
}

// RuntimePath 是打包后应用使用的可写规则副本路径。
func RuntimePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("获取用户配置目录失败: %w", err)
	}
	return filepath.Join(configDir, "pvfine", "objectview.json"), nil
}

// FindSourcePath 定位仓库内的规则文件，开发态改数据即可生效。
func FindSourcePath() (string, bool) {
	starts := make([]string, 0, 2)
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if executable, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(executable))
	}
	seen := make(map[string]struct{})
	for _, start := range starts {
		for dir := filepath.Clean(start); ; dir = filepath.Dir(dir) {
			if _, ok := seen[dir]; !ok {
				seen[dir] = struct{}{}
				candidate := filepath.Join(dir, filepath.FromSlash(sourceRelativePath))
				if fileExists(filepath.Join(dir, "go.mod")) && fileExists(candidate) {
					return candidate, true
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	return "", false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
