package stringguard

import (
	"fmt"
	"os"
	"path/filepath"

	appconfig "pvfine/config"
)

const sourceRelativePath = "config/protected-string-tables.json"

// LoadDefault 用编译进二进制的内置清单构建守卫。
func LoadDefault() (*Guard, error) {
	catalog, err := Parse(appconfig.ProtectedStringTablesJSON)
	if err != nil {
		return nil, err
	}
	return New(catalog), nil
}

// LoadFile 从磁盘加载并校验清单。
func LoadFile(path string) (*Guard, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取字符串表写保护清单失败: %w", err)
	}
	catalog, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return New(catalog), nil
}

// RuntimePath 是打包后应用使用的可写清单副本路径。
func RuntimePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("获取用户配置目录失败: %w", err)
	}
	return filepath.Join(configDir, "pvfine", "protected-string-tables.json"), nil
}

// FindSourcePath 定位仓库内的清单文件，开发态改数据即可生效。
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

// LoadPreferred 优先读仓库内清单（开发态改数据即生效），否则用内置副本。
func LoadPreferred() (*Guard, error) {
	if path, ok := FindSourcePath(); ok {
		if guard, err := LoadFile(path); err == nil {
			return guard, nil
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return LoadDefault()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
