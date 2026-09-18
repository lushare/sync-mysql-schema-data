## sync-mysql-schema-data
基于 [mysql-schema-sync]github.com/hidu/mysql-schema-sync和github.com/weichangdong/sync-mysql-schema-data开发的同步数据的工具.
### 安装
需要有golang的执行环境（直接下载编译好的文件也可以）

```
git clone https://github.com/weichangdong/sync-mysql-schema-data.git
cd sync-mysql-schema-data
go mod tidy
go build main.go
./main -h
```

### 使用
#### 多数用法,参看[mysql-schema-sync](github.com/hidu/mysql-schema-sync)的说明
```
Usage of ./main:
  -conf string
    	json config file path (default "./config.json")
  -dest string
    	mysql dsn dest,eg test@(127.0.0.1:3306)/imis
  -drop
    	drop fields,index,foreign key
  -mail_to string
    	overwrite config's email.to
  -source string
    	mysql dsn source,eg: test@(10.10.0.1:3306)/test
    		when it is not empty ignore [-conf] param
  -sync
    	sync shcema change to dest db
  -sync_data
    	sync source db table data  to dest db table (default true)
  -sync_data_truncate
    	is need truncate  source db table data  to dest db table
  -tables string
    	table names to check
    		eg : product_base,order_*
  -tables_ignore string
    	table names to ignore check and ignore sync data
    		eg : product_base,order_*

mysql schema && data sync tools 0.3
Base On https://github.com/hidu/mysql-schema-sync/
```
#### `sync_data` ./main -sync_data=true,则表示这个操作是同步数据.否则就是同步数据结构.
#### `sync_data_truncate` ./main -sync_data_truncate=true,表示同步源数据的时候,是否truncate本地的数据,没有备份哦,操作需谨慎. 如果不为true,则同步数据的时候,如果目标的数据表,有自增的属性,则id的值是null,否则还是保留原有的id插入.
#### 配置项里面的`sync_data_tables`,指定需要同步数据的数据表.
#### 数据同步时,如果目的库缺表,会自动按源表结构建表后再同步(oracle 源不支持自动建表,会跳过该表并在日志提示).

```
"sync_data_tables":["user_e_trans*","staff_loan_data"],
支持正则了
```
#### Oracle 源库支持(只支持同步数据,不支持结构比对)
完整配置示例,oracle 源同步到 mysql 目的:
```json
{
  "source": "oracle://oracleuser:oraclepasswd@127.0.0.1:1521/ORCL",
  "dest": "root:123456@(127.0.0.1:3306)/dbname?allowNativePasswords=true",
  "tables": ["tab1", "tab2"],
  "tables_ignore": [],
  "sync_data_tables": ["tab1", "tab2"],
  "email": {
    "send_mail": false
  }
}
```
- `source` 写 oracle dsn,以 `oracle://` 前缀区分(驱动为纯 Go 的 go-ora,无需装 oracle 客户端),格式:`oracle://用户名:密码@主机:端口/服务名`;密码里有特殊字符需做 url 转义
- 表清单取自当前用户的 `user_tables`,表名统一转小写后再和 `sync_data_tables` 匹配,配置里写小写表名即可
- 目的库缺表时会自动按源表结构建表(oracle 源不支持自动建表,会跳过该表并在日志提示),mysql 目的表需先建好或用同结构的 mysql 源同步过
- 分页用 `offset N rows fetch next M rows only`,要求 oracle 12c 及以上
- oracle 的 DATE/TIMESTAMP 会以 `yyyy-MM-dd HH:mm:ss` 字面量写入 mysql
- 结构比对(`-sync_data=false`)在 oracle 源下会直接拒绝执行,防止误生成整表 drop


