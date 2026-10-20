-- SQL Server fixture for the screen log type: the oslog objects
-- queries/screen.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; screen.rows.sql then fills them. PatientZero builds its logging
-- schema from this file too (tools/patientzero/internal/logschema).
--
-- The OutSystems 11 DDL from the logging-database survey [3.3]: the logging
-- database keeps 10 cycle tables per log type, oslog_Screen_0 to
-- oslog_Screen_9, one week each, behind two views, oslog_Screen (the
-- current week) and oslog_Screen_Previous (the week before). Two tables
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
CREATE TABLE dbo.oslog_Screen_2 (
    Instant datetime2(3) NOT NULL,
    Duration int NULL,
    Screen varchar(50) NULL,
    Session_Id varchar(50) NOT NULL,
    User_Id int NOT NULL,
    Espace_Id int NOT NULL,
    Tenant_Id int NOT NULL,
    Msisdn varchar(20) NULL,
    Screen_Type varchar(10) NOT NULL,
    Cycle int NOT NULL DEFAULT 2,
    Executed_By varchar(50) NULL,
    Viewstate_Bytes int NOT NULL DEFAULT 0,
    Session_Bytes int NOT NULL DEFAULT 0,
    Session_Requests int NOT NULL DEFAULT 0,
    Access_Mode varchar(10) NOT NULL DEFAULT 'Screen',
    Request_Key varchar(36) NULL,
    Action_Name varchar(60) NULL,
    Client_IP varchar(1000) NULL,
    Espace_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    Username varchar(250) NULL
);
CREATE NONCLUSTERED INDEX idx_oslog_Screen_2_espace_id ON dbo.oslog_Screen_2 (Instant, Espace_Id);
CREATE TABLE dbo.oslog_Screen_3 (
    Instant datetime2(3) NOT NULL,
    Duration int NULL,
    Screen varchar(50) NULL,
    Session_Id varchar(50) NOT NULL,
    User_Id int NOT NULL,
    Espace_Id int NOT NULL,
    Tenant_Id int NOT NULL,
    Msisdn varchar(20) NULL,
    Screen_Type varchar(10) NOT NULL,
    Cycle int NOT NULL DEFAULT 3,
    Executed_By varchar(50) NULL,
    Viewstate_Bytes int NOT NULL DEFAULT 0,
    Session_Bytes int NOT NULL DEFAULT 0,
    Session_Requests int NOT NULL DEFAULT 0,
    Access_Mode varchar(10) NOT NULL DEFAULT 'Screen',
    Request_Key varchar(36) NULL,
    Action_Name varchar(60) NULL,
    Client_IP varchar(1000) NULL,
    Espace_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    Username varchar(250) NULL
);
CREATE NONCLUSTERED INDEX idx_oslog_Screen_3_espace_id ON dbo.oslog_Screen_3 (Instant, Espace_Id);
EXEC (N'CREATE VIEW dbo.oslog_Screen AS
    SELECT Instant, Duration, Screen, Session_Id, User_Id, Espace_Id, Tenant_Id, Msisdn, Screen_Type, Cycle, Executed_By, Viewstate_Bytes, Session_Bytes, Session_Requests, Access_Mode, Request_Key, Action_Name, Client_IP, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_Screen_3');
EXEC (N'CREATE VIEW dbo.oslog_Screen_Previous AS
    SELECT Instant, Duration, Screen, Session_Id, User_Id, Espace_Id, Tenant_Id, Msisdn, Screen_Type, Cycle, Executed_By, Viewstate_Bytes, Session_Bytes, Session_Requests, Access_Mode, Request_Key, Action_Name, Client_IP, Espace_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_Screen_2');
