-- SQL Server fixture for the mobile_request log type: the oslog objects
-- queries/mobile_request.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; mobile_request.rows.sql then fills them.
--
-- A stand-in shaped like OutSystems 11 (TODO 3.3, SQL Server fixture): the
-- logging database keeps 10 cycle tables per log type, oslog_mobile_request_0 to
-- oslog_mobile_request_9, one week each, behind two views, oslog_mobile_request (the current
-- week) and oslog_mobile_request_Previous (the week before). Two tables and the two
-- views are enough for the tests. The columns and their order are the
-- documented fields of this log (OutSystems 11 Log data reference); the SQL
-- Server types are guesses, Instant is datetime. Replace with the real DDL
-- of one cycle table and the two views.
--
-- CREATE VIEW must be alone in its batch and the tests run this file as one
-- batch, so the views are created through EXEC.
CREATE TABLE dbo.oslog_mobile_request_2 (
    Tenant_Id int NULL,
    Id char(36) NOT NULL DEFAULT (CONVERT(char(36), NEWID())),
    Instant datetime NOT NULL,
    Espace_Id int NULL,
    Screen nvarchar(100) NULL,
    Source nvarchar(100) NULL,
    Endpoint nvarchar(500) NULL,
    Duration int NULL,
    Executed_By nvarchar(50) NULL,
    Error_Id int NULL,
    Cycle int NOT NULL DEFAULT 2,
    Request_Key char(36) NULL,
    Login_Id nvarchar(50) NULL,
    User_Id int NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Username nvarchar(100) NULL,
    CONSTRAINT PK_oslog_mobile_request_2 PRIMARY KEY NONCLUSTERED (Id)
);
CREATE CLUSTERED INDEX IX_oslog_mobile_request_2_Instant ON dbo.oslog_mobile_request_2 (Instant);
CREATE TABLE dbo.oslog_mobile_request_3 (
    Tenant_Id int NULL,
    Id char(36) NOT NULL DEFAULT (CONVERT(char(36), NEWID())),
    Instant datetime NOT NULL,
    Espace_Id int NULL,
    Screen nvarchar(100) NULL,
    Source nvarchar(100) NULL,
    Endpoint nvarchar(500) NULL,
    Duration int NULL,
    Executed_By nvarchar(50) NULL,
    Error_Id int NULL,
    Cycle int NOT NULL DEFAULT 3,
    Request_Key char(36) NULL,
    Login_Id nvarchar(50) NULL,
    User_Id int NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Username nvarchar(100) NULL,
    CONSTRAINT PK_oslog_mobile_request_3 PRIMARY KEY NONCLUSTERED (Id)
);
CREATE CLUSTERED INDEX IX_oslog_mobile_request_3_Instant ON dbo.oslog_mobile_request_3 (Instant);
EXEC (N'CREATE VIEW dbo.oslog_mobile_request AS
    SELECT Tenant_Id, Id, Instant, Espace_Id, Screen, Source, Endpoint, Duration, Executed_By, Error_Id, Cycle, Request_Key, Login_Id, User_Id, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_mobile_request_3');
EXEC (N'CREATE VIEW dbo.oslog_mobile_request_Previous AS
    SELECT Tenant_Id, Id, Instant, Espace_Id, Screen, Source, Endpoint, Duration, Executed_By, Error_Id, Cycle, Request_Key, Login_Id, User_Id, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_mobile_request_2');
