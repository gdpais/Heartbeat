-- SQL Server fixture for the screen log type: the oslog objects
-- queries/screen.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; screen.rows.sql then fills them.
--
-- A stand-in shaped like OutSystems 11 (TODO 3.3, SQL Server fixture): the
-- logging database keeps 10 cycle tables per log type, oslog_Screen_0 to
-- oslog_Screen_9, one week each, behind two views, oslog_Screen (the current
-- week) and oslog_Screen_Previous (the week before). Two tables and the two
-- views are enough for the tests. The columns and their order are the
-- documented fields of this log (OutSystems 11 Log data reference); the SQL
-- Server types are guesses, Instant is datetime. Replace with the real DDL
-- of one cycle table and the two views.
--
-- CREATE VIEW must be alone in its batch and the tests run this file as one
-- batch, so the views are created through EXEC.
CREATE TABLE dbo.oslog_Screen_2 (
    Instant datetime NOT NULL,
    Duration int NULL,
    Screen nvarchar(100) NULL,
    Session_Id nvarchar(50) NULL,
    User_Id int NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Msisdn nvarchar(50) NULL,
    Screen_Type nvarchar(50) NULL,
    Cycle int NOT NULL DEFAULT 2,
    Executed_By nvarchar(50) NULL,
    Session_Bytes int NULL,
    Viewstate_Bytes int NULL,
    Session_Requests int NULL,
    Access_Mode nvarchar(50) NULL,
    Request_Key char(36) NULL,
    Action_Name nvarchar(100) NULL,
    Client_IP nvarchar(50) NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Username nvarchar(100) NULL
);
CREATE CLUSTERED INDEX IX_oslog_Screen_2_Instant ON dbo.oslog_Screen_2 (Instant);
CREATE TABLE dbo.oslog_Screen_3 (
    Instant datetime NOT NULL,
    Duration int NULL,
    Screen nvarchar(100) NULL,
    Session_Id nvarchar(50) NULL,
    User_Id int NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Msisdn nvarchar(50) NULL,
    Screen_Type nvarchar(50) NULL,
    Cycle int NOT NULL DEFAULT 3,
    Executed_By nvarchar(50) NULL,
    Session_Bytes int NULL,
    Viewstate_Bytes int NULL,
    Session_Requests int NULL,
    Access_Mode nvarchar(50) NULL,
    Request_Key char(36) NULL,
    Action_Name nvarchar(100) NULL,
    Client_IP nvarchar(50) NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Username nvarchar(100) NULL
);
CREATE CLUSTERED INDEX IX_oslog_Screen_3_Instant ON dbo.oslog_Screen_3 (Instant);
EXEC (N'CREATE VIEW dbo.oslog_Screen AS
    SELECT Instant, Duration, Screen, Session_Id, User_Id, Espace_Id, Tenant_Id, Msisdn, Screen_Type, Cycle, Executed_By, Session_Bytes, Viewstate_Bytes, Session_Requests, Access_Mode, Request_Key, Action_Name, Client_IP, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_Screen_3');
EXEC (N'CREATE VIEW dbo.oslog_Screen_Previous AS
    SELECT Instant, Duration, Screen, Session_Id, User_Id, Espace_Id, Tenant_Id, Msisdn, Screen_Type, Cycle, Executed_By, Session_Bytes, Viewstate_Bytes, Session_Requests, Access_Mode, Request_Key, Action_Name, Client_IP, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_Screen_2');
