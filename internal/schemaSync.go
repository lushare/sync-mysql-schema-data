package internal

import (
	"fmt"
	"log"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SchemaSync 配置文件
type SchemaSync struct {
	Config   *Config
	SourceDb *MyDb
	DestDb   *MyDb
}

// syncDataBatchRows 数据同步每页(批)的行数,一页合并成一条多值 insert
// 批越大网络往返越少,但单条语句也越大;过大撞上 max_allowed_packet 时会自动降级逐行插入
const syncDataBatchRows = 2000

// NewSchemaSync 对一个配置进行同步
func NewSchemaSync(config *Config) *SchemaSync {
	s := new(SchemaSync)
	s.Config = config
	s.SourceDb = NewMyDb(config.SourceDSN, "source")
	s.DestDb = NewMyDb(config.DestDSN, "dest")
	return s
}

// GetNewTableNames 获取所有新增加的表名
func (sc *SchemaSync) GetNewTableNames() []string {
	sourceTables := sc.SourceDb.GetTableNames()
	destTables := sc.DestDb.GetTableNames()

	var newTables []string

	for _, name := range sourceTables {
		if !inStringSlice(name, destTables) {
			newTables = append(newTables, name)
		}
	}
	return newTables
}

func (sc *SchemaSync) getAlterDataByTable(table string) *TableAlterData {
	alter := new(TableAlterData)
	alter.Table = table
	alter.Type = alterTypeNo

	sschema := sc.SourceDb.GetTableSchema(table)
	dschema := sc.DestDb.GetTableSchema(table)

	alter.SchemaDiff = newSchemaDiff(table, sschema, dschema)

	if sschema == dschema {
		return alter
	}
	if sschema == "" {
		alter.Type = alterTypeDrop
		alter.SQL = fmt.Sprintf("drop table `%s`;", table)
		return alter
	}
	if dschema == "" {
		alter.Type = alterTypeCreate
		alter.SQL = sschema + ";"
		return alter
	}

	diff := sc.getSchemaDiff(alter)
	if diff != "" {
		alter.Type = alterTypeAlter
		alter.SQL = fmt.Sprintf("ALTER TABLE `%s`\n%s;", table, diff)
	}

	return alter
}

func (sc *SchemaSync) getSchemaDiff(alter *TableAlterData) string {
	sourceMyS := alter.SchemaDiff.Source
	destMyS := alter.SchemaDiff.Dest
	table := alter.Table

	var alterLines []string
	//比对字段
	for name, dt := range sourceMyS.Fields {
		if sc.Config.IsIgnoreField(table, name) {
			log.Printf("ignore column %s.%s", table, name)
			continue
		}
		var alterSQL string
		if destDt, has := destMyS.Fields[name]; has {
			if dt != destDt {
				alterSQL = fmt.Sprintf("CHANGE `%s` %s", name, dt)
			}
		} else {
			alterSQL = "ADD " + dt
		}
		if alterSQL != "" {
			//log.Println("trace check column.alter ", fmt.Sprintf("%s.%s", table, name), "alterSQL=", alterSQL)
			alterLines = append(alterLines, alterSQL)
		} else {
			//log.Println("trace check column.alter ", fmt.Sprintf("%s.%s", table, name), "not change")
		}
	}

	//源库已经删除的字段
	if sc.Config.Drop {
		for name := range destMyS.Fields {
			if sc.Config.IsIgnoreField(table, name) {
				log.Printf("ignore column %s.%s", table, name)
				continue
			}
			if _, has := sourceMyS.Fields[name]; !has {
				alterSQL := fmt.Sprintf("drop `%s`", name)
				alterLines = append(alterLines, alterSQL)
				//log.Println("trace check column.drop ", fmt.Sprintf("%s.%s", table, name), "alterSQL=", alterSQL)
			} else {
				//log.Println("trace check column.drop ", fmt.Sprintf("%s.%s", table, name), "not change")
			}
		}
	}

	//多余的字段暂不删除

	//比对索引
	for indexName, idx := range sourceMyS.IndexAll {
		if sc.Config.IsIgnoreIndex(table, indexName) {
			log.Printf("ignore index %s.%s", table, indexName)
			continue
		}
		dIdx, has := destMyS.IndexAll[indexName]
		//log.Println("trace indexName---->[", fmt.Sprintf("%s.%s", table, indexName), "] dest_has:", has, "\ndest_idx:", dIdx, "\nsource_idx:", idx)
		alterSQL := ""
		if has {
			if idx.SQL != dIdx.SQL {
				alterSQL = idx.alterAddSQL(true)
			}
		} else {
			alterSQL = idx.alterAddSQL(false)
		}
		if alterSQL != "" {
			alterLines = append(alterLines, alterSQL)
			//log.Println("trace check index.alter ", fmt.Sprintf("%s.%s", table, indexName), "alterSQL=", alterSQL)
		} else {
			//log.Println("trace check index.alter ", fmt.Sprintf("%s.%s", table, indexName), "not change")
		}
	}

	//drop index
	if sc.Config.Drop {
		for indexName, dIdx := range destMyS.IndexAll {
			if sc.Config.IsIgnoreIndex(table, indexName) {
				log.Printf("ignore index %s.%s", table, indexName)
				continue
			}
			var dropSQL string
			if _, has := sourceMyS.IndexAll[indexName]; !has {
				dropSQL = dIdx.alterDropSQL()
			}

			if dropSQL != "" {
				alterLines = append(alterLines, dropSQL)
				log.Println("trace check index.drop ", fmt.Sprintf("%s.%s", table, indexName), "alterSQL=", dropSQL)
			} else {
				log.Println("trace check index.drop ", fmt.Sprintf("%s.%s", table, indexName), " not change")
			}
		}
	}

	//比对外键
	for foreignName, idx := range sourceMyS.ForeignAll {
		if sc.Config.IsIgnoreForeignKey(table, foreignName) {
			log.Printf("ignore foreignName %s.%s", table, foreignName)
			continue
		}
		dIdx, has := destMyS.ForeignAll[foreignName]
		log.Println("trace foreignName---->[", fmt.Sprintf("%s.%s", table, foreignName), "] dest_has:", has, "\ndest_idx:", dIdx, "\nsource_idx:", idx)
		alterSQL := ""
		if has {
			if idx.SQL != dIdx.SQL {
				alterSQL = idx.alterAddSQL(true)
			}
		} else {
			alterSQL = idx.alterAddSQL(false)
		}
		if alterSQL != "" {
			alterLines = append(alterLines, alterSQL)
			//log.Println("trace check foreignKey.alter ", fmt.Sprintf("%s.%s", table, foreignName), "alterSQL=", alterSQL)
		} else {
			//log.Println("trace check foreignKey.alter ", fmt.Sprintf("%s.%s", table, foreignName), "not change")
		}
	}

	//drop 外键
	if sc.Config.Drop {
		for foreignName, dIdx := range destMyS.ForeignAll {
			if sc.Config.IsIgnoreForeignKey(table, foreignName) {
				log.Printf("ignore foreignName %s.%s", table, foreignName)
				continue
			}
			var dropSQL string
			if _, has := sourceMyS.ForeignAll[foreignName]; !has {
				log.Println("trace foreignName --->[", fmt.Sprintf("%s.%s", table, foreignName), "]", "didx:", dIdx)
				dropSQL = dIdx.alterDropSQL()

			}
			if dropSQL != "" {
				alterLines = append(alterLines, dropSQL)
				//log.Println("trace check foreignKey.drop ", fmt.Sprintf("%s.%s", table, foreignName), "alterSQL=", dropSQL)
			} else {
				//log.Println("trace check foreignKey.drop ", fmt.Sprintf("%s.%s", table, foreignName), "not change")
			}
		}
	}

	return strings.Join(alterLines, ",\n")
}

// SyncSQL4Dest sync schema change
func (sc *SchemaSync) SyncSQL4Dest(sqlStr string, sqls []string) error {
	log.Println("Exec_SQL_START:\n>>>>>>\n", sqlStr, "\n<<<<<<<<")
	sqlStr = strings.TrimSpace(sqlStr)
	if sqlStr == "" {
		log.Println("sql_is_empty,skip")
		return nil
	}
	t := newMyTimer()
	ret, err := sc.DestDb.Query(sqlStr)

	//how to enable allowMultiQueries?
	if err != nil && len(sqls) > 1 {
		log.Println("exec_mut_query failed,err=", err, ",now exec sqls foreach")
		tx, errTx := sc.DestDb.Db.Begin()
		if errTx == nil {
			for _, sql := range sqls {
				ret, err = tx.Query(sql)
				log.Println("query_one:[", sql, "]", err)
				if err != nil {
					break
				}
			}
			if err == nil {
				err = tx.Commit()
			} else {
				tx.Rollback()
			}
		}
	}
	t.stop()
	if err != nil {
		log.Println("EXEC_SQL_FAIELD", err)
		return err
	}
	defer ret.Close()
	log.Println("EXEC_SQL_SUCCESS,used:", t.usedSecond())
	cl, err := ret.Columns()
	log.Println("EXEC_SQL_RET:", cl, err)
	return err
}

// CheckSchemaDiff 执行最终的diff
func CheckSchemaDiff(cfg *Config) {
	// oracle 没有 show create table,结构链路取不到源表结构,继续跑会给目的库生成整表 drop,必须显式拦下
	if strings.HasPrefix(cfg.SourceDSN, "oracle://") {
		log.Fatalln("[CheckSchemaDiff] oracle source not support schema diff,only support sync_data")
	}
	statics := newStatics(cfg)
	defer (func() {
		statics.timer.stop()
		statics.sendMailNotice(cfg)
	})()

	sc := NewSchemaSync(cfg)
	newTables := sc.SourceDb.GetTableNames()
	log.Println("source db table total:", len(newTables))

	changedTables := make(map[string][]*TableAlterData)

	for _, table := range newTables {
		//log.Printf("Index : %d Table : %s\n", index, table)
		if !cfg.CheckMatchTables(table) {
			//log.Println("Table:", table, "skip")
			continue
		}

		if cfg.CheckMatchIgnoreTables(table) == true {
			//log.Println("Table:", table, "skip")
			continue
		}

		sd := sc.getAlterDataByTable(table)

		if sd.Type != alterTypeNo {
			fmt.Println(sd)
			fmt.Println("")
			relationTables := sd.SchemaDiff.RelationTables()
			//			fmt.Println("relationTables:",table,relationTables)

			//将所有有外键关联的单独放
			groupKey := "multi"
			if len(relationTables) == 0 {
				groupKey = "single_" + table
			}
			if _, has := changedTables[groupKey]; !has {
				changedTables[groupKey] = make([]*TableAlterData, 0)
			}
			changedTables[groupKey] = append(changedTables[groupKey], sd)
		} else {
			//log.Println("table:", table, "not change,", sd)
		}
	}

	//log.Println("trace changedTables:", changedTables)

	countSuccess := 0
	countFailed := 0
	canRunTypePref := "single"
	//先执行单个表的
run_sync:
	for typeName, sds := range changedTables {
		if !strings.HasPrefix(typeName, canRunTypePref) {
			continue
		}
		log.Println("runSyncType:", typeName)
		var sqls []string
		var sts []*tableStatics
		for _, sd := range sds {
			sql := strings.TrimRight(sd.SQL, ";")
			sqls = append(sqls, sql)

			st := statics.newTableStatics(sd.Table, sd)
			sts = append(sts, st)
		}

		sql := strings.Join(sqls, ";\n") + ";"
		var ret error

		if sc.Config.Sync {

			ret = sc.SyncSQL4Dest(sql, sqls)
			if ret == nil {
				countSuccess++
			} else {
				countFailed++
			}
		}
		for _, st := range sts {
			st.alterRet = ret
			st.schemaAfter = sc.DestDb.GetTableSchema(st.table)
			st.timer.stop()
		}

	} //end for

	//最后再执行多个表的alter
	if canRunTypePref == "single" {
		canRunTypePref = "multi"
		goto run_sync
	}

	if sc.Config.Sync {
		log.Println("execute_all_sql_done,success_total:", countSuccess, "failed_total:", countFailed)
	}

}

// 把配置里面的表的数据 同步到目标数据库
func SyncTableData(cfg *Config) {
	statics := newStatics(cfg)
	defer (func() {
		statics.timer.stop()
	})()
	sc := NewSchemaSync(cfg)

	needSyncDataTables := cfg.SyncDataTables
	log.Println("[SyncTableData] tables:", needSyncDataTables)
	if len(needSyncDataTables) <= 0 {
		log.Println("[SyncTableData] no tables need sync")
		return
	}

	// 源数据库所有的表
	allSourceTables := sc.SourceDb.GetTableNames()
	needSyncDataTablesOk := []string{}
	for _, tableTmp := range allSourceTables {
		if cfg.CheckMatchSyncTables(tableTmp) == false {
			continue
		}
		needSyncDataTablesOk = append(needSyncDataTablesOk, tableTmp)
	}

		// 每次同步多少条
		var limitNum float64 = syncDataBatchRows
	for _, oneTable := range needSyncDataTablesOk {
		if cfg.CheckMatchIgnoreTables(oneTable) == true {
			log.Println("[SyncTableData] ignore table:", oneTable)
			continue
		}
		if cfg.SyncDataTruncate == true {
			_, err := sc.DestDb.Db.Exec("truncate  table " + oneTable)
			if err != nil {
				log.Println("[SyncTableData] truncate table error :", oneTable, " error:", err)
				continue
			}
			log.Println("[SyncTableData] truncate table:", oneTable)
		}
		//查询数据表 是否自增
		hasAutoIncrement := true
		sqlTableStatus := fmt.Sprintf("show  table  status where  Name='%s'", oneTable)
		tableStatusData := sc.DestDb.QueryAll(sqlTableStatus)
		if len(tableStatusData) == 0 {
			// 目的库没有这张表,按源表结构先建表,建不出来才跳过
			if !sc.createDestTableIfNotExists(oneTable) {
				continue
			}
			tableStatusData = sc.DestDb.QueryAll(sqlTableStatus)
			if len(tableStatusData) == 0 {
				log.Println("[SyncTableData] table created but show table status still empty:", oneTable)
				continue
			}
		}
		autoIncrement := tableStatusData[0]["Auto_increment"]
		dataType := reflect.TypeOf(autoIncrement)
		if dataType == nil {
			hasAutoIncrement = false
		}
		// 查询总行数
		// count 的返回类型随驱动不同(mysql 是 []byte,oracle 是 int64),统一扫描成 interface{} 再转换
		sqlCount := fmt.Sprintf("select count(1) as total_num from %s", oneTable)
		rsCount := sc.SourceDb.Db.QueryRow(sqlCount)
		var countVal interface{}
		if err := rsCount.Scan(&countVal); err != nil {
			log.Println("[SyncTableData] get table count failed:", oneTable, " error:", err)
			continue
		}
		totalNum, countOk := toFloat64(countVal)
		if !countOk {
			log.Println("[SyncTableData] unexpected table count value:", oneTable, " value:", countVal)
			continue
		}

		totalTimes := math.Ceil(totalNum / limitNum)
		var limitStart, i float64 = 0, 0
		okNum := 0
		for ; i < totalTimes; i++ {
			limitStart = i * limitNum
			var sql string
			if sc.SourceDb.IsOracle() {
				// oracle 12c+ 分页写法;与 mysql 路径一样不带 order by,页间顺序不做保证
				sql = fmt.Sprintf("select * from %s offset %v rows fetch next %v rows only", oneTable, limitStart, limitNum)
			} else {
				sql = fmt.Sprintf("select * from %s limit %v,%v", oneTable, limitStart, limitNum)
			}
			valObjs := sc.SourceDb.QueryAll(sql)
			if len(valObjs) > 0 {
				// 一页合并成一条多值 insert,减少网络往返;批量失败(如超过 max_allowed_packet)时降级回逐行插入,保住能插的行
				batchSql := buildBatchInsertSql(oneTable, valObjs, cfg.SyncDataTruncate, hasAutoIncrement)
				if _, batchErr := sc.DestDb.Db.Exec(batchSql); batchErr != nil {
					log.Println("[SyncTableData] batch insert failed,fallback to row insert. table:", oneTable, " error:", batchErr)
					for _, valObj := range valObjs {
						insertSql := buildInsertSql(oneTable, valObj, cfg.SyncDataTruncate, hasAutoIncrement)
						insertResult, insertErr := sc.DestDb.Db.Exec(insertSql)
						if insertResult == nil || insertErr != nil {
							log.Println("[SyncTableData] insert error:", insertErr, " table:", oneTable, "sql:", insertSql)
							continue
						}
						insertId, insertErr := insertResult.LastInsertId()
						insertAffectedNum, _ := insertResult.RowsAffected()
						if (insertId == 0 && insertAffectedNum == 0) || insertErr != nil {
							log.Println("[SyncTableData] insert error:", insertErr, " insertId:", insertId, " insertAffectedNum:", insertAffectedNum, " table:", oneTable)
							continue
						}
						okNum++
					}
				} else {
					okNum += len(valObjs)
				}
			}
			// 按页打进度,避免大表长时间无输出看起来像卡死
			log.Println("[SyncTableData] table :", oneTable, " progress:", int(i+1), "/", int(totalTimes), " pages, copied:", okNum)
		}

		log.Println("[SyncTableData] table :", oneTable, " totalNum:", totalNum, " okNum:", okNum)
	}
}

// createDestTableIfNotExists 目的库缺表时,取源表的建表语句在目的库原样创建
// 返回 false 表示表没法就绪(拿不到源表结构或建表失败),调用方应跳过该表
func (sc *SchemaSync) createDestTableIfNotExists(table string) bool {
	if sc.SourceDb.IsOracle() {
		// oracle 没有 show create table,拿不到建表语句,无法自动建表
		log.Println("[SyncTableData] dest table not found,auto create table not support oracle source,skip:", table)
		return false
	}
	schema := sc.SourceDb.GetTableSchema(table)
	if schema == "" {
		log.Println("[SyncTableData] dest table not found and get source schema empty,skip:", table)
		return false
	}
	log.Println("[SyncTableData] dest table not found,auto create it from source schema:", table)
	return sc.SyncSQL4Dest(schema+";", []string{schema}) == nil
}

// getSortedKeys 返回按列名排序的 key 列表,保证同一批行的列顺序一致
func getSortedKeys(insertTmp map[string]interface{}) []string {
	sortedKeys := make([]string, 0, len(insertTmp))
	for k := range insertTmp {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)
	return sortedKeys
}

// buildInsertFields 生成 `c1`,`c2`,... 形式的列清单
func buildInsertFields(insertTmp map[string]interface{}) string {
	fields := make([]string, 0, len(insertTmp))
	for _, k := range getSortedKeys(insertTmp) {
		fields = append(fields, "`"+k+"`")
	}
	return strings.Join(fields, ",")
}

// buildInsertValueTuple 生成一行的 (v1,v2,...) 值串,列顺序与 buildInsertFields 一致
func buildInsertValueTuple(insertTmp map[string]interface{}, truncate bool, hasAutoIncrement bool) string {
	sortedKeys := getSortedKeys(insertTmp)
	totalNum := len(insertTmp)
	suffix := ","
	valueOk := ""
	for num, k := range sortedKeys {
		if totalNum-1 == num {
			suffix = ""
		}
		v := insertTmp[k]
		// oracle 源返回的列名是大写,自增 id 的判断统一用忽略大小写比较
		isIdCol := strings.EqualFold(k, "id")
		switch v.(type) {
		case int:
			if truncate == false && isIdCol && hasAutoIncrement == true {
				valueOk += "null" + suffix
			} else {
				valueOk += Int2Str(v.(int)) + suffix
			}
		case int64:
			if truncate == false && isIdCol && hasAutoIncrement == true {
				valueOk += "null" + suffix
			} else {
				valueOk += Int642Str(v.(int64)) + suffix
			}
		case float64:
			valueOk += Float642Str(v.(float64)) + suffix
		case float32:
			valueOk += Float322Str(v.(float32)) + suffix
		case time.Time:
			// oracle 的 DATE/TIMESTAMP 经驱动返回 time.Time,按 mysql 目标库可接受的字面量格式写入
			valueOk += "'" + v.(time.Time).Format("2006-01-02 15:04:05") + "'" + suffix
		case string:
			if truncate == false && isIdCol && hasAutoIncrement == true {
				valueOk += "null" + suffix
			} else {
				valueOk += "'" + strings.Replace(v.(string), "'", `\'`, -1) + "'" + suffix
			}
		case nil:
			valueOk += "NULL" + suffix
		}
	}
	return "(" + valueOk + ")"
}

func buildInsertSql(tableName string, insertTmp map[string]interface{}, truncate bool, hasAutoIncrement bool) string {
	return "insert into " + tableName + "(" + buildInsertFields(insertTmp) + ") values " + buildInsertValueTuple(insertTmp, truncate, hasAutoIncrement) + ";"
}

// buildBatchInsertSql 把一页多行合并成一条 insert into t(...) values (...),(...);
// 大幅减少网络往返;单行超长(如超大 text)导致整条超过 max_allowed_packet 时会失败,由调用方降级逐行插入
func buildBatchInsertSql(tableName string, rows []map[string]interface{}, truncate bool, hasAutoIncrement bool) string {
	if len(rows) == 0 {
		return ""
	}
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		values = append(values, buildInsertValueTuple(row, truncate, hasAutoIncrement))
	}
	return "insert into " + tableName + "(" + buildInsertFields(rows[0]) + ") values " + strings.Join(values, ",") + ";"
}

func Str2Int64(str string) (int64, error) {
	number, err := strconv.ParseInt(str, 10, 64)
	return number, err
}

// toFloat64 把数据库驱动返回的数值统一转成 float64
// 兼容 mysql(count 返回 []byte)与 oracle(返回 int64/float64)等驱动差异
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case []byte:
		f, err := Str2Float64(string(n))
		return f, err == nil
	case string:
		f, err := Str2Float64(n)
		return f, err == nil
	}
	return 0, false
}

func Int642Str(number int64) string {
	return strconv.FormatInt(number, 10)
}

func Str2Int(str string) (int, error) {
	number, err := strconv.ParseInt(str, 10, 0)
	return int(number), err
}

func Int2Str(number int) string {
	return strconv.FormatInt(int64(number), 10)
}

func Float2Str(f float32) string {
	return Float642Str(float64(f))
}

func Float642Str(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func Float322Str(f float32) string {
	return strconv.FormatFloat(float64(f), 'f', -1, 32)
}

func Str2Float64(s string) (f float64, err error) {
	f, err = strconv.ParseFloat(s, 64)

	return
}

func Str2Float(s string) (f float32, err error) {
	f64, err := Str2Float64(s)
	f = float32(f64)

	return
}
