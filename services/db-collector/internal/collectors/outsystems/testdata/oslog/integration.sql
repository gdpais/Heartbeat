-- SQL Server fixture for the integration log type: the oslog objects
-- queries/integration.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; integration.rows.sql then fills them.
--
-- A stand-in shaped like OutSystems 11 (TODO 3.3, SQL Server fixture): the
-- logging database keeps 10 cycle tables per log type, oslog_Integration_0 to
-- oslog_Integration_9, one week each, behind two views, oslog_Integration (the current
-- week) and oslog_Integration_Previous (the week before). Two tables and the two
-- views are enough for the tests. The columns and their order are the
-- documented fields of this log (OutSystems 11 Log data reference); the SQL
-- Server types are guesses, Instant is datetime. Replace with the real DDL
-- of one cycle table and the two views.
--
-- CREATE VIEW must be alone in its batch and the tests run this file as one
-- batch, so the views are created through EXEC.
CREATE TABLE dbo.oslog_Integration_2 (
    Id char(36) NOT NULL DEFAULT (CONVERT(char(36), NEWID())),
    Instant datetime NOT NULL,
    Duration int NULL,
    Source nvarchar(100) NULL,
    Endpoint nvarchar(500) NULL,
    Action nvarchar(100) NULL,
    Type nvarchar(50) NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Error_Id nvarchar(36) NULL,
    Executed_By nvarchar(50) NULL,
    Cycle int NOT NULL DEFAULT 2,
    Is_Expose bit NULL,
    Request_Key char(36) NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    CONSTRAINT PK_oslog_Integration_2 PRIMARY KEY NONCLUSTERED (Id)
);
CREATE CLUSTERED INDEX IX_oslog_Integration_2_Instant ON dbo.oslog_Integration_2 (Instant);
CREATE TABLE dbo.oslog_Integration_3 (
    Id char(36) NOT NULL DEFAULT (CONVERT(char(36), NEWID())),
    Instant datetime NOT NULL,
    Duration int NULL,
    Source nvarchar(100) NULL,
    Endpoint nvarchar(500) NULL,
    Action nvarchar(100) NULL,
    Type nvarchar(50) NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Error_Id nvarchar(36) NULL,
    Executed_By nvarchar(50) NULL,
    Cycle int NOT NULL DEFAULT 3,
    Is_Expose bit NULL,
    Request_Key char(36) NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    CONSTRAINT PK_oslog_Integration_3 PRIMARY KEY NONCLUSTERED (Id)
);
CREATE CLUSTERED INDEX IX_oslog_Integration_3_Instant ON dbo.oslog_Integration_3 (Instant);
EXEC (N'CREATE VIEW dbo.oslog_Integration AS
    SELECT Id, Instant, Duration, Source, Endpoint, Action, Type, Espace_Id, Tenant_Id, Error_Id, Executed_By, Cycle, Is_Expose, Request_Key, Espace_Name, Application_Name, Application_Key
    FROM dbo.oslog_Integration_3');
EXEC (N'CREATE VIEW dbo.oslog_Integration_Previous AS
    SELECT Id, Instant, Duration, Source, Endpoint, Action, Type, Espace_Id, Tenant_Id, Error_Id, Executed_By, Cycle, Is_Expose, Request_Key, Espace_Name, Application_Name, Application_Key
    FROM dbo.oslog_Integration_2');
