// 独立验证工具：只读检查任意 lignting 数据目录的 SQLite 状态。
// 用法：go run ./tests/verify/dbcheck <path-to-lignting.db>
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: dbcheck <db-path>")
		os.Exit(2)
	}
	dsn := "file:" + os.Args[1] + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	defer db.Close()
	ctx := context.Background()
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		fmt.Println("journal_mode:", err)
	} else {
		fmt.Println("journal_mode =", mode)
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		fmt.Println("query:", err)
		os.Exit(1)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Printf("tables(%d) = %v\n", len(names), names)
	var version int
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err == nil {
		fmt.Println("schema_version(max) =", version)
	} else {
		fmt.Println("schema_migrations 不可读:", err)
	}
	fi, _ := os.Stat(os.Args[1])
	fmt.Println("db_bytes =", fi.Size())
}
