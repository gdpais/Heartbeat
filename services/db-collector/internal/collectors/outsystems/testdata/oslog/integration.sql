-- SQL Server fixture for the integration log type: the oslog objects
-- queries/integration.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; integration.rows.sql then fills them. PatientZero builds its logging
-- schema from this file too (tools/patientzero/internal/logschema).
--
-- The OutSystems 11 DDL from the logging-database survey [3.3]: the logging
-- database keeps 10 cycle tables per log type, oslog_Integration_0 to
-- oslog_Integration_9, one week each, behind two views, oslog_Integration (the
-- current week) and oslog_Integration_Previous (the week before). Two tables
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
CREATE TABLE dbo.oslog_Integration_2 (
    Id varchar(50) NOT NULL,
    Instant datetime2(3) NULL,
    Duration int NULL,
    Source varchar(1000) NULL,
    Endpoint varchar(1000) NULL,
    Action varchar(100) NULL,
    Type varchar(50) NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Error_Id varchar(50) NULL,
    Executed_by varchar(50) NULL,
    Is_Expose bit NULL,
    Cycle int NULL DEFAULT 2,
    REQUEST_KEY varchar(36) NULL,
    Espace_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    CONSTRAINT PK_oslog_Integration_2_Id PRIMARY KEY CLUSTERED (Id)
);
CREATE NONCLUSTERED INDEX idx_oslog_Integration_2_espace_id ON dbo.oslog_Integration_2 (Instant, Espace_Id);
CREATE TABLE dbo.oslog_Integration_3 (
    Id varchar(50) NOT NULL,
    Instant datetime2(3) NULL,
    Duration int NULL,
    Source varchar(1000) NULL,
    Endpoint varchar(1000) NULL,
    Action varchar(100) NULL,
    Type varchar(50) NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Error_Id varchar(50) NULL,
    Executed_by varchar(50) NULL,
    Is_Expose bit NULL,
    Cycle int NULL DEFAULT 3,
    REQUEST_KEY varchar(36) NULL,
    Espace_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    CONSTRAINT PK_oslog_Integration_3_Id PRIMARY KEY CLUSTERED (Id)
);
CREATE NONCLUSTERED INDEX idx_oslog_Integration_3_espace_id ON dbo.oslog_Integration_3 (Instant, Espace_Id);
EXEC (N'CREATE VIEW dbo.oslog_Integration AS
    SELECT Id, Instant, Duration, Source, Endpoint, Action, Type, Espace_Id, Tenant_Id, Error_Id, Executed_by, Is_Expose, Cycle, REQUEST_KEY, Espace_Name, Application_Name, Application_Key
    FROM dbo.oslog_Integration_3');
EXEC (N'CREATE VIEW dbo.oslog_Integration_Previous AS
    SELECT Id, Instant, Duration, Source, Endpoint, Action, Type, Espace_Id, Tenant_Id, Error_Id, Executed_by, Is_Expose, Cycle, REQUEST_KEY, Espace_Name, Application_Name, Application_Key
    FROM dbo.oslog_Integration_2');
