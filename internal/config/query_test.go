package config

import (
	"strings"
	"testing"
)

func TestValidateReadOnlySelectAccepts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{"plain SELECT", "SELECT 1"},
		{"lower case", "select status, count(*) as n from sys.dm_exec_sessions group by status"},
		{"trailing semicolon", "SELECT 1;"},
		{"trailing semicolon and comments", "SELECT 1; -- done\n/* really */ ;"},
		{"leading semicolon before WITH", ";WITH a AS (SELECT 1 AS x) SELECT x FROM a"},
		{"CTE", "WITH a AS (SELECT 1 AS x), b (y) AS (SELECT x FROM a) SELECT y FROM b"},
		{"XML namespaces", "WITH XMLNAMESPACES ('urn:x' AS x) SELECT 1"},
		{"UNION ALL", "SELECT 1 UNION ALL SELECT 2 UNION SELECT 3 EXCEPT SELECT 4 INTERSECT SELECT 5"},
		{"subqueries", "SELECT (SELECT 1), x FROM (SELECT 2 AS x) AS t WHERE EXISTS (SELECT 1) AND x IN (SELECT 2)"},
		{"CASE END", "SELECT CASE WHEN 1 = 1 THEN CASE 2 WHEN 2 THEN 'a' END ELSE 'b' END"},
		{"OFFSET FETCH", "SELECT name FROM sys.databases ORDER BY name OFFSET 0 ROWS FETCH NEXT 5 ROWS ONLY"},
		{"keyword in a string", "SELECT 'DROP TABLE x; EXEC xp_cmdshell ''dir''' AS s"},
		{"keyword in a Unicode string", "SELECT N'; DELETE FROM t' AS s"},
		{"keyword in a line comment", "SELECT 1 -- then DROP TABLE x; INSERT INTO y\nFROM sys.objects"},
		{"keyword in a nested block comment", "SELECT /* outer /* inner DROP */ still a comment; EXEC x */ 1"},
		{"keywords as bracketed names", "SELECT [Insert], [Drop]] me], [sp_count], [x;y] FROM [Backup].[Delete]"},
		{"keywords as quoted names", `SELECT "Update", "say ""set""" FROM t`},
		{"keyword after a dot", "SELECT t.status, t.open_time, r.set_options FROM t JOIN sys.dm_exec_requests AS r ON 1 = 1"},
		{"variables and parameters", "SELECT TOP (@limit) @@VERSION, Instant FROM dbo.v WHERE Instant > @from AND Instant <= @to ORDER BY Instant"},
		{"XML methods", "SELECT x.value('(./Record/@id)[1]', 'int') FROM t CROSS APPLY t.r.nodes('/x') AS n(x)"},
		{"live demo divide by zero", "SELECT 1 AS blocking_session_id, 1/0 AS blocked_count"},
		{"numbers", "SELECT 1.5, 2e3, 2E-3, 1., 0x1F, .5, $1.00, 1FROM sys.objects"},
		{"tabs and CRLF", "SELECT\t1\r\nFROM t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateReadOnlySelect(tc.query); err != nil {
				t.Fatalf("ValidateReadOnlySelect(%q) = %v, want nil", tc.query, err)
			}
		})
	}
}

func TestValidateReadOnlySelectRejects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{"empty", "  -- nothing\n ; ", "is empty"},
		{"two statements", "SELECT 1; SELECT 2", "more than one statement"},
		{"statement after a semicolon", "SELECT 1; DROP TABLE x", "more than one statement"},
		{"statement after a comment and semicolon", "SELECT 1 /* ok */; -- still\n DELETE FROM t", "more than one statement"},
		{"statement hidden after a line comment", "SELECT 1 -- comment\nDROP TABLE x", "contains DROP"},
		{"statement hidden after a block comment", "SELECT 1 /* comment */DROP TABLE x", "contains DROP"},
		{"second SELECT without a semicolon", "SELECT 1 SELECT 2", "a second top-level SELECT"},
		{"second SELECT after a CTE query", "WITH a AS (SELECT 1 AS x) SELECT x FROM a SELECT 2", "a second top-level SELECT"},
		{"does not start with SELECT", "EXEC sp_who", "must start with SELECT or WITH"},
		{"bare procedure", "sp_who2", "must start with SELECT or WITH"},
		{"WITH without SELECT", "WITH a AS (SELECT 1 AS x) DELETE FROM a", "contains DELETE"},
		{"WITH without any top-level SELECT", "WITH a AS (SELECT 1 AS x)", "no top-level SELECT"},
		{"INSERT", "SELECT 1 INSERT INTO t VALUES (1)", "contains INSERT"},
		{"UPDATE", "SELECT 1 UPDATE t SET x = 1", "contains UPDATE"},
		{"DELETE", "SELECT 1 DELETE t", "contains DELETE"},
		{"MERGE", "SELECT 1 MERGE t USING s ON 1 = 1", "contains MERGE"},
		{"DROP", "SELECT 1 DROP TABLE t", "contains DROP"},
		{"ALTER", "SELECT 1 ALTER LOGIN sa ENABLE", "contains ALTER"},
		{"CREATE", "SELECT 1 CREATE TABLE t (x int)", "contains CREATE"},
		{"TRUNCATE", "SELECT 1 TRUNCATE TABLE t", "contains TRUNCATE"},
		{"EXEC", "SELECT 1 EXEC ('DROP TABLE t')", "contains EXEC"},
		{"EXECUTE", "select 1 execute sp_configure", "contains EXECUTE"},
		{"GRANT", "SELECT 1 GRANT CONTROL SERVER TO x", "contains GRANT"},
		{"DENY", "SELECT 1 DENY SELECT ON t TO x", "contains DENY"},
		{"REVOKE", "SELECT 1 REVOKE SELECT ON t FROM x", "contains REVOKE"},
		{"SELECT INTO", "SELECT * INTO #copy FROM sys.objects", "contains INTO"},
		{"OPENROWSET", "SELECT * FROM OPENROWSET('SQLNCLI', 'Server=x', 'SELECT 1')", "contains OPENROWSET"},
		{"OPENQUERY", "SELECT * FROM OPENQUERY(remote, 'SELECT 1')", "contains OPENQUERY"},
		{"OPENDATASOURCE", "SELECT * FROM OPENDATASOURCE('SQLNCLI', 'Data Source=x').db.dbo.t", "contains OPENDATASOURCE"},
		{"extended procedure", "SELECT * FROM master.dbo.xp_cmdshell", "sp_ or xp_"},
		{"extended procedure after two dots", "SELECT * FROM master..XP_REGREAD", "sp_ or xp_"},
		{"system procedure", "SELECT * FROM sys.sp_who", "sp_ or xp_"},
		{"WAITFOR", "SELECT 1 WAITFOR DELAY '00:10:00'", "contains WAITFOR"},
		{"SHUTDOWN", "SELECT 1 SHUTDOWN WITH NOWAIT", "contains SHUTDOWN"},
		{"DBCC", "SELECT 1 DBCC FREEPROCCACHE", "contains DBCC"},
		{"BACKUP", "SELECT 1 BACKUP DATABASE x TO DISK = 'x'", "contains BACKUP"},
		{"RESTORE", "SELECT 1 RESTORE DATABASE x FROM DISK = 'x'", "contains RESTORE"},
		{"USE", "SELECT 1 USE master", "contains USE"},
		{"SET", "SELECT 1 SET LOCK_TIMEOUT -1", "contains SET"},
		{"transaction", "SELECT 1 BEGIN TRAN", "contains BEGIN"},
		{"KILL", "SELECT 1 KILL 52", "contains KILL"},
		{"END without CASE", "SELECT 1 END CONVERSATION @h", "END without CASE"},
		{"keyword after a number and a dot", "SELECT 1. EXEC('DROP TABLE dbo.t')", "contains EXEC"},
		{"keyword glued to a number and a dot", "SELECT 1.DROP TABLE dbo.t", "contains DROP"},
		{"keyword glued to a number", "SELECT 1DROP TABLE dbo.t", "contains DROP"},
		{"keyword glued to a comparison", "SELECT 1 AS x FROM sys.objects WHERE 1 = 1DELETE dbo.t", "contains DELETE"},
		{"keyword glued to an exponent", "SELECT 1e5WAITFOR DELAY '00:00:20'", "contains WAITFOR"},
		{"keyword glued to a hex literal", "SELECT 0x1FGRANT CONTROL SERVER TO x", "contains GRANT"},
		{"keyword as a column part", "SELECT t.SHUTDOWN FROM t", "contains SHUTDOWN"},
		{"END after a number and a dot", "SELECT 1. END CONVERSATION @h", "END without CASE"},
		{"SELECT after a number and a dot", "SELECT 1. SELECT 2", "a second top-level SELECT"},
		{"line separator ending a comment", "SELECT 1 --\u2028DROP TABLE t", "line-separator"},
		{"next line ending a comment", "SELECT 1 --\u0085DROP TABLE t", "line-separator"},
		{"form feed", "SELECT 1\fFROM t", "control"},
		{"unbalanced closing parenthesis", "SELECT (1))", "unbalanced parentheses"},
		{"unbalanced opening parenthesis", "SELECT ((1)", "unbalanced parentheses"},
		{"unterminated string", "SELECT 'abc", "unterminated string"},
		{"unterminated string with escapes", "SELECT 'it''s; DROP TABLE x", "unterminated string"},
		{"unterminated bracket", "SELECT [abc FROM t", "unterminated [bracketed]"},
		{"unterminated quoted identifier", `SELECT "abc FROM t`, "unterminated \"quoted\""},
		{"unterminated block comment", "SELECT 1 /* open", "unterminated /* comment */"},
		{"unterminated nested block comment", "SELECT 1 /* a /* b */ DROP TABLE x", "unterminated /* comment */"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateReadOnlySelect(tc.query)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateReadOnlySelect(%q) = %v, want an error containing %q", tc.query, err, tc.want)
			}
		})
	}
}
