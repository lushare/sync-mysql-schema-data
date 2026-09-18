package internal

import (
	"database/sql"
	"fmt"
	"strings"

	//load mysql
	"log"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/sijms/go-ora/v2"
)

// dialectOracle oracle 数据库方言标识,对应 dsn 前缀 oracle://
const dialectOracle = "oracle"

// MyDb db struct
type MyDb struct {
	Db      *sql.DB
	dbType  string
	dialect string
}

// NewMyDb parse dsn
func NewMyDb(dsn string, dbType string) *MyDb {
	dialect := "mysql"
	if strings.HasPrefix(dsn, "oracle://") {
		dialect = dialectOracle
	}
	db, err := sql.Open(dialect, dsn)
	if err != nil {
		panic(fmt.Sprintf("connect to db [%s] failed,%v", dsn, err))
	}
	return &MyDb{
		Db:      db,
		dbType:  dbType,
		dialect: dialect,
	}
}

// IsOracle 是否 oracle 数据库
func (mydb *MyDb) IsOracle() bool {
	return mydb.dialect == dialectOracle
}

// GetTableNames table names
func (mydb *MyDb) GetTableNames() []string {
	if mydb.IsOracle() {
		return mydb.getOracleTableNames()
	}
	rs, err := mydb.Query("show table status")
	if err != nil {
		panic("show tables failed:" + err.Error())
	}
	defer rs.Close()
	tables := []string{}
	columns, _ := rs.Columns()
	for rs.Next() {
		var values = make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}
		if err := rs.Scan(valuePtrs...); err != nil {
			panic("show tables failed when scan," + err.Error())
		}
		var valObj = make(map[string]interface{})
		for i, col := range columns {
			var v interface{}
			val := values[i]
			b, ok := val.([]byte)
			if ok {
				v = string(b)
			} else {
				v = val
			}
			valObj[col] = v
		}
		if valObj["Engine"] != nil {
			tables = append(tables, valObj["Name"].(string))
		}
	}
	return tables
}

// getOracleTableNames oracle 库取当前用户的表清单
// 统一转小写:配置里的表名按小写匹配,且小写表名不加引号写在 SQL 里,oracle 会自动折叠成大写,两种场景都能对上
func (mydb *MyDb) getOracleTableNames() []string {
	rs, err := mydb.Query("select lower(table_name) from user_tables")
	if err != nil {
		panic("get oracle table names failed:" + err.Error())
	}
	defer rs.Close()
	tables := []string{}
	for rs.Next() {
		var name string
		if err := rs.Scan(&name); err != nil {
			panic("scan oracle table name failed," + err.Error())
		}
		tables = append(tables, name)
	}
	return tables
}

// GetTableSchema table schema
func (mydb *MyDb) GetTableSchema(name string) (schema string) {
	rs, err := mydb.Query(fmt.Sprintf("show create table `%s`", name))
	if err != nil {
		log.Println(err)
		return
	}
	defer rs.Close()
	for rs.Next() {
		var vname string
		if err := rs.Scan(&vname, &schema); err != nil {
			panic(fmt.Sprintf("get table %s 's schema failed,%s", name, err))
		}
	}
	return
}

// Query execute sql query
func (mydb *MyDb) Query(query string, args ...interface{}) (*sql.Rows, error) {
	log.Println("[SQL]", "["+mydb.dbType+"]", query, args)
	return mydb.Db.Query(query, args...)
}

func (mydb *MyDb) QueryAll(sql string) []map[string]interface{} {
	rs, err := mydb.Db.Query(sql)
	if err != nil {
		log.Println("[QueryAll] exec failed:", sql, " error:", err)
		return nil
	}
	defer rs.Close()
	columns, _ := rs.Columns()
	var okData []map[string]interface{}
	for rs.Next() {
		var values = make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}
		rs.Scan(valuePtrs...)
		var valObj = make(map[string]interface{})
		for i, col := range columns {
			var v interface{}
			val := values[i]
			b, ok := val.([]byte)
			if ok {
				v = string(b)
			} else {
				v = val
			}
			valObj[col] = v
		}
		okData = append(okData, valObj)
	}
	return okData
}
