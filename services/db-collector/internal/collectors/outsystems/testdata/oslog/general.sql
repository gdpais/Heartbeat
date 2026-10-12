-- SQL Server fixture for the general log type: the oslog objects
-- queries/general.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; general.rows.sql then fills them.
--
-- A stand-in shaped like OutSystems 11 (TODO 3.3, SQL Server fixture): the
-- logging database keeps 10 cycle tables per log type, oslog_General_0 to
-- oslog_General_9, one week each, behind two views, oslog_General (the current
-- week) and oslog_General_Previous (the week before). Two tables and the two
-- views are enough for the tests. The columns and their order are the
-- documented fields of this log (OutSystems 11 Log data reference); the SQL
-- Server types are guesses, Instant is datetime. Replace with the real DDL
-- of one cycle table and the two views.
--
-- CREATE VIEW must be alone in its batch and the tests run this file as one
-- batch, so the views are created through EXEC.
CREATE TABLE dbo.oslog_General_2 (
    Instant datetime NOT NULL,
    Session_Id nvarchar(50) NULL,
    User_Id int NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Error_Id nvarchar(36) NULL,
    Message nvarchar(2000) NULL,
    Message_Type nvarchar(50) NULL,
    Module_Name nvarchar(50) NULL,
    Cycle int NOT NULL DEFAULT 2,
    Request_Key char(36) NULL,
    Action_Name nvarchar(100) NULL,
    Entrypoint_Name nvarchar(100) NULL,
    Client_IP nvarchar(50) NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Username nvarchar(100) NULL
);
CREATE CLUSTERED INDEX IX_oslog_General_2_Instant ON dbo.oslog_General_2 (Instant);
CREATE TABLE dbo.oslog_General_3 (
    Instant datetime NOT NULL,
    Session_Id nvarchar(50) NULL,
    User_Id int NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Error_Id nvarchar(36) NULL,
    Message nvarchar(2000) NULL,
    Message_Type nvarchar(50) NULL,
    Module_Name nvarchar(50) NULL,
    Cycle int NOT NULL DEFAULT 3,
    Request_Key char(36) NULL,
    Action_Name nvarchar(100) NULL,
    Entrypoint_Name nvarchar(100) NULL,
    Client_IP nvarchar(50) NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Username nvarchar(100) NULL
);
CREATE CLUSTERED INDEX IX_oslog_General_3_Instant ON dbo.oslog_General_3 (Instant);
EXEC (N'CREATE VIEW dbo.oslog_General AS
    SELECT Instant, Session_Id, User_Id, Espace_Id, Tenant_Id, Error_Id, Message, Message_Type, Module_Name, Cycle, Request_Key, Action_Name, Entrypoint_Name, Client_IP, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_General_3');
EXEC (N'CREATE VIEW dbo.oslog_General_Previous AS
    SELECT Instant, Session_Id, User_Id, Espace_Id, Tenant_Id, Error_Id, Message, Message_Type, Module_Name, Cycle, Request_Key, Action_Name, Entrypoint_Name, Client_IP, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_General_2');
