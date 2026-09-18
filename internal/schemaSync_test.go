package internal

import (
	"testing"
	"time"
)

func TestBuildInsertSqlOracleTypes(t *testing.T) {
	tm := time.Date(2026, 9, 18, 10, 30, 0, 0, time.UTC)
	// oracle 源返回的列名是大写;truncate=true 时自增 id 也保留原值
	row := map[string]interface{}{
		"ID":          int64(5),
		"NAME":        "x'y",
		"CREATE_TIME": tm,
		"MEMO":        nil,
	}
	sql := buildInsertSql("model_t", row, true, true)
	want := "insert into model_t(`CREATE_TIME`,`ID`,`MEMO`,`NAME`) values ('2026-09-18 10:30:00',5,NULL,'x\\'y');"
	if sql != want {
		t.Fatalf("buildInsertSql result:\n got: %s\nwant: %s", sql, want)
	}
}

func TestBuildInsertSqlAutoIncrementIdIgnoreCase(t *testing.T) {
	// 列名大写的 ID 也要命中自增 id 置 null 的逻辑
	sql := buildInsertSql("model_t", map[string]interface{}{"ID": int64(5)}, false, true)
	want := "insert into model_t(`ID`) values (null);"
	if sql != want {
		t.Fatalf("buildInsertSql result:\n got: %s\nwant: %s", sql, want)
	}
	// 小写 id 保持原行为
	sql = buildInsertSql("model_t", map[string]interface{}{"id": int64(5)}, false, true)
	want = "insert into model_t(`id`) values (null);"
	if sql != want {
		t.Fatalf("buildInsertSql result:\n got: %s\nwant: %s", sql, want)
	}
}

func TestBuildBatchInsertSql(t *testing.T) {
	// select * 出来的同一批行列集一定一致
	rows := []map[string]interface{}{
		{"ID": int64(1), "NAME": "a"},
		{"ID": int64(2), "NAME": "b'"},
		{"ID": nil, "NAME": nil},
	}
	sql := buildBatchInsertSql("model_t", rows, true, true)
	want := "insert into model_t(`ID`,`NAME`) values (1,'a'),(2,'b\\''),(NULL,NULL);"
	if sql != want {
		t.Fatalf("buildBatchInsertSql result:\n got: %s\nwant: %s", sql, want)
	}
	if buildBatchInsertSql("model_t", nil, true, true) != "" {
		t.Fatal("empty rows should return empty sql")
	}
}

func TestToFloat64(t *testing.T) {
	cases := []struct {
		in   interface{}
		want float64
		ok   bool
	}{
		{int64(7), 7, true},
		{int(7), 7, true},
		{float64(7.5), 7.5, true},
		{[]byte("123"), 123, true},
		{"456", 456, true},
		{nil, 0, false},
		{struct{}{}, 0, false},
	}
	for _, c := range cases {
		got, ok := toFloat64(c.in)
		if ok != c.ok || got != c.want {
			t.Fatalf("toFloat64(%#v) = (%v,%v), want (%v,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
