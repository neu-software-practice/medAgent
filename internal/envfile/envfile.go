// Package envfile 提供零依赖的 .env 文件加载器，将 KEY=value 行设为环境变量。
// 已有环境变量不会被覆盖（外部优先），文件不存在时静默跳过。
package envfile

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Load 从 path 指向的 .env 文件加载环境变量。path 为空时默认读当前工作目录下的 .env。
// 文件不存在时不报错（静默跳过），解析错误返回明确错误。
// 已存在的环境变量不会被覆盖。
func Load(path string) error {
	if path == "" {
		path = filepath.Join(cwd(), ".env")
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("envfile: 打开 %s 失败: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue // 无 = 的行跳过，不视为错误
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			continue
		}
		val := strings.TrimSpace(line[eq+1:])
		// 去掉首尾引号（双引号或单引号）
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		// 不覆盖已存在的环境变量
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("envfile: 读取 %s 失败 (第 %d 行): %w", path, lineNo, err)
	}
	return nil
}

// cwd 返回当前工作目录。调用失败时回退为 "."。
func cwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

// Default 返回环境变量 key 的值；若未设置，返回 def。
// 与 os.Getenv 不同：显式设为空字符串（KEY=）会保留空值，不会被 def 覆盖。
func Default(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
