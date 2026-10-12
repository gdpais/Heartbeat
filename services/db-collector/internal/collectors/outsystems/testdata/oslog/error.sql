-- SQL Server fixture for the error log type: the oslog objects
-- queries/error.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; error.rows.sql then fills them.
--
-- A stand-in shaped like OutSystems 11 (TODO 3.3, SQL Server fixture): the
-- logging database keeps 10 cycle tables per log type, oslog_Error_0 to
-- oslog_Error_9, one week each, behind two views, oslog_Error (the current
-- week) and oslog_Error_Previous (the week before). Two tables and the two
-- views are enough for the tests. The columns and their order are the
-- documented fields of this log (OutSystems 11 Log data reference); the SQL
-- Server types are guesses, Instant is datetime. Replace with the real DDL
-- of one cycle table and the two views.
--
-- CREATE VIEW must be alone in its batch and the tests run this file as one
-- batch, so the views are created through EXEC.
CREATE TABLE dbo.oslog_Error_2 (
    Id char(36) NOT NULL DEFAULT (CONVERT(char(36), NEWID())),
    Instant datetime NOT NULL,
    Session_Id nvarchar(50) NULL,
    User_Id int NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Message nvarchar(2000) NULL,
    Stack nvarchar(max) NULL,
    Module_Name nvarchar(50) NULL,
    Server nvarchar(50) NULL,
    Cycle int NOT NULL DEFAULT 2,
    Environment_Information nvarchar(max) NULL,
    Request_key char(36) NULL,
    Entrypoint_Name nvarchar(100) NULL,
    Action_Name nvarchar(100) NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Username nvarchar(100) NULL,
    CONSTRAINT PK_oslog_Error_2 PRIMARY KEY NONCLUSTERED (Id)
);
CREATE CLUSTERED INDEX IX_oslog_Error_2_Instant ON dbo.oslog_Error_2 (Instant);
CREATE TABLE dbo.oslog_Error_3 (
    Id char(36) NOT NULL DEFAULT (CONVERT(char(36), NEWID())),
    Instant datetime NOT NULL,
    Session_Id nvarchar(50) NULL,
    User_Id int NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Message nvarchar(2000) NULL,
    Stack nvarchar(max) NULL,
    Module_Name nvarchar(50) NULL,
    Server nvarchar(50) NULL,
    Cycle int NOT NULL DEFAULT 3,
    Environment_Information nvarchar(max) NULL,
    Request_key char(36) NULL,
    Entrypoint_Name nvarchar(100) NULL,
    Action_Name nvarchar(100) NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Username nvarchar(100) NULL,
    CONSTRAINT PK_oslog_Error_3 PRIMARY KEY NONCLUSTERED (Id)
);
CREATE CLUSTERED INDEX IX_oslog_Error_3_Instant ON dbo.oslog_Error_3 (Instant);
EXEC (N'CREATE VIEW dbo.oslog_Error AS
    SELECT Id, Instant, Session_Id, User_Id, Espace_Id, Tenant_Id, Message, Stack, Module_Name, Server, Cycle, Environment_Information, Request_key, Entrypoint_Name, Action_Name, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_Error_3');
EXEC (N'CREATE VIEW dbo.oslog_Error_Previous AS
    SELECT Id, Instant, Session_Id, User_Id, Espace_Id, Tenant_Id, Message, Stack, Module_Name, Server, Cycle, Environment_Information, Request_key, Entrypoint_Name, Action_Name, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_Error_2');
