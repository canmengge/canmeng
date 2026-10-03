package formview

import (
	"fmt"
	"os"
	"path/filepath"

	appconfig "pvfine/config"
)

const sourceRelativePath = "config/formats.json"

// LoadDefault 用编译进二进制的内置规则构建目录。
func LoadDefault() (Catalog, error) {
	return Parse(appconfig.FormatsJSON)
}

// LoadFile 从磁盘加载并校验规则。
func LoadFile(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("读取结构化视图规则失败: %w", err)
	}
	return Parse(data)
}

// RuntimePath 是打包后应用使用的可写规则副本路径。
func RuntimePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("获取用户配置目录失败: %w", err)
	}
	return filepath.Join(configDir, "pvfine", "formats.json"), nil
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
				if hasGoMod(filepath.Join(dir, "go.mod")) && hasFile(candidate) {
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

func hasFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func hasGoMod(path string) bool {
	return hasFile(path)
}
