-- SQL Server fixture for the extension log type: the oslog objects
-- queries/extension.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; extension.rows.sql then fills them. PatientZero builds its logging
-- schema from this file too (tools/patientzero/internal/logschema).
--
-- The OutSystems 11 DDL from the logging-database survey [3.3]: the logging
-- database keeps 10 cycle tables per log type, oslog_Extension_0 to
-- oslog_Extension_9, one week each, behind two views, oslog_Extension (the
-- current week) and oslog_Extension_Previous (the week before). Two tables
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
CREATE TABLE dbo.oslog_Extension_2 (
    Instant datetime2(3) NOT NULL,
    Duration int NULL,
    Action_Name varchar(50) NOT NULL,
    Session_Id varchar(24) NOT NULL,
    User_Id int NOT NULL,
    Espace_Id int NOT NULL,
    Tenant_Id int NOT NULL,
    Extension_Id int NOT NULL,
    Executed_By varchar(50) NOT NULL,
    Error_Id varchar(50) NULL,
    Cycle int NOT NULL DEFAULT 2,
    Request_Key varchar(36) NULL,
    Espace_Name varchar(50) NULL,
    Extension_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    Username varchar(250) NULL
);
CREATE NONCLUSTERED INDEX idx_oslog_Extension_2_extension_id ON dbo.oslog_Extension_2 (Instant, Extension_Id, Espace_Id);
CREATE TABLE dbo.oslog_Extension_3 (
    Instant datetime2(3) NOT NULL,
    Duration int NULL,
    Action_Name varchar(50) NOT NULL,
    Session_Id varchar(24) NOT NULL,
    User_Id int NOT NULL,
    Espace_Id int NOT NULL,
    Tenant_Id int NOT NULL,
    Extension_Id int NOT NULL,
    Executed_By varchar(50) NOT NULL,
    Error_Id varchar(50) NULL,
    Cycle int NOT NULL DEFAULT 3,
    Request_Key varchar(36) NULL,
    Espace_Name varchar(50) NULL,
    Extension_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    Username varchar(250) NULL
);
CREATE NONCLUSTERED INDEX idx_oslog_Extension_3_extension_id ON dbo.oslog_Extension_3 (Instant, Extension_Id, Espace_Id);
EXEC (N'CREATE VIEW dbo.oslog_Extension AS
    SELECT Instant, Duration, Action_Name, Session_Id, User_Id, Espace_Id, Tenant_Id, Extension_Id, Executed_By, Error_Id, Cycle, Request_Key, Espace_Name, Extension_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_Extension_3');
EXEC (N'CREATE VIEW dbo.oslog_Extension_Previous AS
    SELECT Instant, Duration, Action_Name, Session_Id, User_Id, Espace_Id, Tenant_Id, Extension_Id, Executed_By, Error_Id, Cycle, Request_Key, Espace_Name, Extension_Name, Application_Name, Application_Key, Username
    FROM dbo.oslog_Extension_2');
