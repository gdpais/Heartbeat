-- SQL Server fixture for the extension log type: the oslog objects
-- queries/extension.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; extension.rows.sql then fills them.
--
-- A stand-in shaped like OutSystems 11 (TODO 3.3, SQL Server fixture): the
-- logging database keeps 10 cycle tables per log type, oslog_Extension_0 to
-- oslog_Extension_9, one week each, behind two views, oslog_Extension (the current
-- week) and oslog_Extension_Previous (the week before). Two tables and the two
-- views are enough for the tests. The columns and their order are the
-- documented fields of this log (OutSystems 11 Log data reference); the SQL
-- Server types are guesses, Instant is datetime. Replace with the real DDL
-- of one cycle table and the two views.
--
-- CREATE VIEW must be alone in its batch and the tests run this file as one
-- batch, so the views are created through EXEC.
CREATE TABLE dbo.oslog_Extension_2 (
    Instant datetime NOT NULL,
    Duration int NULL,
    Action_Name nvarchar(100) NULL,
    Session_Id nvarchar(50) NULL,
    User_Id int NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Extension_Id int NULL,
    Executed_By nvarchar(50) NULL,
    Error_Id nvarchar(36) NULL,
    Cycle int NOT NULL DEFAULT 2,
    Request_Key char(36) NULL,
    Espace_Name nvarchar(50) NULL,
    Extension_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Username nvarchar(100) NULL
);
CREATE CLUSTERED INDEX IX_oslog_Extension_2_Instant ON dbo.oslog_Extension_2 (Instant);
CREATE TABLE dbo.oslog_Extension_3 (
    Instant datetime NOT NULL,
    Duration int NULL,
    Action_Name nvarchar(100) NULL,
    Session_Id nvarchar(50) NULL,
    User_Id int NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Extension_Id int NULL,
    Executed_By nvarchar(50) NULL,
    Error_Id nvarchar(36) NULL,
    Cycle int NOT NULL DEFAULT 3,
    Request_Key char(36) NULL,
    Espace_Name nvarchar(50) NULL,
    Extension_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Username nvarchar(100) NULL
);
CREATE CLUSTERED INDEX IX_oslog_Extension_3_Instant ON dbo.oslog_Extension_3 (Instant);
EXEC (N'CREATE VIEW dbo.oslog_Extension AS
    SELECT Instant, Duration, Action_Name, Session_Id, User_Id, Espace_Id, Tenant_Id, Extension_Id, Executed_By, Error_Id, Cycle, Request_Key, Espace_Name, Extension_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_Extension_3');
EXEC (N'CREATE VIEW dbo.oslog_Extension_Previous AS
    SELECT Instant, Duration, Action_Name, Session_Id, User_Id, Espace_Id, Tenant_Id, Extension_Id, Executed_By, Error_Id, Cycle, Request_Key, Espace_Name, Extension_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_Extension_2');
