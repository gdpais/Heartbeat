-- SQL Server fixture for the mobile request log type: the oslog objects
-- queries/mobile_request.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; mobile_request.rows.sql then fills them. PatientZero builds its logging
-- schema from this file too (tools/patientzero/internal/logschema).
--
-- The OutSystems 11 DDL from the logging-database survey [3.3]: the logging
-- database keeps 10 cycle tables per log type, oslog_mobile_request_0 to
-- oslog_mobile_request_9, one week each, behind two views, oslog_mobile_request (the
-- current week) and oslog_mobile_request_Previous (the week before). Two tables
-- and the two views are enough for the tests. Column names, order, types,
-- nullability, defaults and indexes are the surveyed database's, except:
-- - datetime2 precision was not surveyed; (3) is inferred from the
--   millisecond timestamps the surveyed database held;
-- - the platform's views are SELECT * over one cycle table; these list the
--   columns, which is the same thing and lets PatientZero read them;
-- - Cycle has a default, so an insert need not name it.
--
-- CREATE VIEW must be alone in its batch and the tests run this file as one
-- batch, so the views are created through EXEC.
CREATE TABLE dbo.oslog_mobile_request_2 (
    Id varchar(50) NOT NULL,
    Instant datetime2(3) NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Screen varchar(1000) NULL,
    Endpoint varchar(1000) NULL,
    Source varchar(1000) NULL,
    Duration int NULL,
    Executed_by varchar(50) NULL,
    Error_Id varchar(50) NULL,
    Cycle int NULL DEFAULT 2,
    Request_Key varchar(36) NULL,
    Login_Id varchar(60) NULL,
    User_Id int NOT NULL DEFAULT 0,
    Espace_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    Username varchar(250) NULL,
    CONSTRAINT PK_oslog_mobile_request_2_Id PRIMARY KEY CLUSTERED (Id)
);
CREATE NONCLUSTERED INDEX idx_oslog_mobile_request_2_espace_id ON dbo.oslog_mobile_request_2 (Instant, Espace_Id);
CREATE TABLE dbo.oslog_mobile_request_3 (
    Id varchar(50) NOT NULL,
    Instant datetime2(3) NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Screen varchar(1000) NULL,
    Endpoint varchar(1000) NULL,
    Source varchar(1000) NULL,
    Duration int NULL,
    Executed_by varchar(50) NULL,
    Error_Id varchar(50) NULL,
    Cycle int NULL DEFAULT 3,
    Request_Key varchar(36) NULL,
    Login_Id varchar(60) NULL,
    User_Id int NOT NULL DEFAULT 0,
    Espace_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    Username varchar(250) NULL,
    CONSTRAINT PK_oslog_mobile_request_3_Id PRIMARY KEY CLUSTERED (Id)
);
CREATE NONCLUSTERED INDEX idx_oslog_mobile_request_3_espace_id ON dbo.oslog_mobile_request_3 (Instant, Espace_Id);
EXEC (N'CREATE VIEW dbo.oslog_mobile_request AS
    SELECT Id, Instant, Espace_Id, Tenant_Id, Screen, Endpoint, Source, Duration, Executed_by, Error_Id, Cycle, Request_Key, Login_Id, User_Id, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_mobile_request_3');
EXEC (N'CREATE VIEW dbo.oslog_mobile_request_Previous AS
    SELECT Id, Instant, Espace_Id, Tenant_Id, Screen, Endpoint, Source, Duration, Executed_by, Error_Id, Cycle, Request_Key, Login_Id, User_Id, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_mobile_request_2');
