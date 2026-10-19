-- SQL Server fixture for the error log type: the oslog objects
-- queries/error.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; error.rows.sql then fills them. PatientZero builds its logging
-- schema from this file too (tools/patientzero/internal/logschema).
--
-- The OutSystems 11 DDL from the logging-database survey [3.3]: the logging
-- database keeps 10 cycle tables per log type, oslog_Error_0 to
-- oslog_Error_9, one week each, behind two views, oslog_Error (the
-- current week) and oslog_Error_Previous (the week before). Two tables
-- and the two views are enough for the tests. Column names, order, types,
-- nullability, defaults and indexes are the surveyed database's, except:
-- - datetime2 precision was not surveyed; (3) is inferred from the
--   millisecond timestamps the surveyed database held;
-- - the views list their columns, which lets PatientZero read them (the
--   other log types' platform views are SELECT * over one cycle table);
-- - Cycle has a default, so an insert need not name it;
-- - what the platform leaves in the cycle table's Stack when the full trace
--   is in the detail table was not surveyed; the rows put its first 2000
--   characters there.
--
-- As on the platform, oslog_Error left-joins oslog_Error_Detail_<cycle>,
-- which holds the full stack trace of an error whose trace is longer than
-- the cycle table's Stack, and returns that as Stack. The view's Stack is
-- computed, so an error row cannot be inserted through the view: a writer
-- inserts into the current cycle table and, for a long trace, into its
-- detail table.
--
-- CREATE VIEW must be alone in its batch and the tests run this file as one
-- batch, so the views are created through EXEC.
CREATE TABLE dbo.oslog_Error_2 (
    Id varchar(50) NOT NULL,
    Instant datetime2(3) NOT NULL,
    Session_Id varchar(50) NOT NULL,
    User_Id int NOT NULL,
    Espace_Id int NOT NULL,
    Tenant_Id int NOT NULL,
    Message varchar(2000) NOT NULL,
    Stack varchar(2000) NULL,
    Module_Name varchar(15) NOT NULL,
    Server varchar(50) NOT NULL,
    Cycle int NOT NULL DEFAULT 2,
    EnvironmentInformation varchar(2000) NULL,
    Request_Key varchar(36) NULL,
    Entrypoint_Name varchar(60) NULL,
    Action_Name varchar(60) NULL,
    Espace_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    Username varchar(250) NULL,
    CONSTRAINT PK_oslog_Error_2 PRIMARY KEY CLUSTERED (Id)
);
CREATE NONCLUSTERED INDEX idx_oslog_Error_2_espace_id ON dbo.oslog_Error_2 (Instant, Espace_Id);
CREATE TABLE dbo.oslog_Error_Detail_2 (
    Id varchar(50) NOT NULL,
    FullStackTrace varchar(max) NOT NULL,
    CONSTRAINT PK_oslog_Error_Detail_2 PRIMARY KEY CLUSTERED (Id)
);
CREATE TABLE dbo.oslog_Error_3 (
    Id varchar(50) NOT NULL,
    Instant datetime2(3) NOT NULL,
    Session_Id varchar(50) NOT NULL,
    User_Id int NOT NULL,
    Espace_Id int NOT NULL,
    Tenant_Id int NOT NULL,
    Message varchar(2000) NOT NULL,
    Stack varchar(2000) NULL,
    Module_Name varchar(15) NOT NULL,
    Server varchar(50) NOT NULL,
    Cycle int NOT NULL DEFAULT 3,
    EnvironmentInformation varchar(2000) NULL,
    Request_Key varchar(36) NULL,
    Entrypoint_Name varchar(60) NULL,
    Action_Name varchar(60) NULL,
    Espace_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    Username varchar(250) NULL,
    CONSTRAINT PK_oslog_Error_3 PRIMARY KEY CLUSTERED (Id)
);
CREATE NONCLUSTERED INDEX idx_oslog_Error_3_espace_id ON dbo.oslog_Error_3 (Instant, Espace_Id);
CREATE TABLE dbo.oslog_Error_Detail_3 (
    Id varchar(50) NOT NULL,
    FullStackTrace varchar(max) NOT NULL,
    CONSTRAINT PK_oslog_Error_Detail_3 PRIMARY KEY CLUSTERED (Id)
);
EXEC (N'CREATE VIEW dbo.oslog_Error AS
    SELECT error.Id, error.Instant, error.Session_Id, error.User_Id, error.Espace_Id, error.Tenant_Id, error.Message,
        CASE WHEN errorDetail.FullStackTrace IS NULL THEN error.Stack ELSE errorDetail.FullStackTrace END AS Stack,
        error.Module_Name, error.Server, error.Cycle, error.EnvironmentInformation, error.Entrypoint_Name, error.Action_Name, error.Request_Key, error.Espace_Name, error.Application_Name, error.Application_Key, error.Username
    FROM dbo.oslog_Error_3 error
    LEFT JOIN dbo.oslog_Error_Detail_3 errorDetail ON errorDetail.Id = error.Id');
EXEC (N'CREATE VIEW dbo.oslog_Error_Previous AS
    SELECT error.Id, error.Instant, error.Session_Id, error.User_Id, error.Espace_Id, error.Tenant_Id, error.Message,
        CASE WHEN errorDetail.FullStackTrace IS NULL THEN error.Stack ELSE errorDetail.FullStackTrace END AS Stack,
        error.Module_Name, error.Server, error.Cycle, error.EnvironmentInformation, error.Entrypoint_Name, error.Action_Name, error.Request_Key, error.Espace_Name, error.Application_Name, error.Application_Key, error.Username
    FROM dbo.oslog_Error_2 error
    LEFT JOIN dbo.oslog_Error_Detail_2 errorDetail ON errorDetail.Id = error.Id');
