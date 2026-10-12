-- SQL Server fixture for the cyclic_job log type: the oslog objects
-- queries/cyclic_job.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; cyclic_job.rows.sql then fills them.
--
-- A stand-in shaped like OutSystems 11 (TODO 3.3, SQL Server fixture): the
-- logging database keeps 10 cycle tables per log type, oslog_Cyclic_Job_0 to
-- oslog_Cyclic_Job_9, one week each, behind two views, oslog_Cyclic_Job (the current
-- week) and oslog_Cyclic_Job_Previous (the week before). Two tables and the two
-- views are enough for the tests. The columns and their order are the
-- documented fields of this log (OutSystems 11 Log data reference); the SQL
-- Server types are guesses, Instant is datetime. Replace with the real DDL
-- of one cycle table and the two views.
--
-- CREATE VIEW must be alone in its batch and the tests run this file as one
-- batch, so the views are created through EXEC.
CREATE TABLE dbo.oslog_Cyclic_Job_2 (
    Instant datetime NOT NULL,
    Duration int NULL,
    Cyclic_Job_Key char(36) NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Executed_By nvarchar(50) NULL,
    Error_Id nvarchar(36) NULL,
    Should_Have_Run_At datetime NULL,
    Next_Run datetime NULL,
    Cycle int NOT NULL DEFAULT 2,
    Request_Key char(36) NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Cyclic_Job_Name nvarchar(100) NULL
);
CREATE CLUSTERED INDEX IX_oslog_Cyclic_Job_2_Instant ON dbo.oslog_Cyclic_Job_2 (Instant);
CREATE TABLE dbo.oslog_Cyclic_Job_3 (
    Instant datetime NOT NULL,
    Duration int NULL,
    Cyclic_Job_Key char(36) NULL,
    Espace_Id int NULL,
    Tenant_Id int NULL,
    Executed_By nvarchar(50) NULL,
    Error_Id nvarchar(36) NULL,
    Should_Have_Run_At datetime NULL,
    Next_Run datetime NULL,
    Cycle int NOT NULL DEFAULT 3,
    Request_Key char(36) NULL,
    Espace_Name nvarchar(50) NULL,
    Application_Name nvarchar(100) NULL,
    Application_Key char(36) NULL,
    Cyclic_Job_Name nvarchar(100) NULL
);
CREATE CLUSTERED INDEX IX_oslog_Cyclic_Job_3_Instant ON dbo.oslog_Cyclic_Job_3 (Instant);
EXEC (N'CREATE VIEW dbo.oslog_Cyclic_Job AS
    SELECT Instant, Duration, Cyclic_Job_Key, Espace_Id, Tenant_Id, Executed_By, Error_Id, Should_Have_Run_At, Next_Run, Cycle, Request_Key, Espace_Name, Application_Name, Application_Key, Cyclic_Job_Name
    FROM dbo.oslog_Cyclic_Job_3');
EXEC (N'CREATE VIEW dbo.oslog_Cyclic_Job_Previous AS
    SELECT Instant, Duration, Cyclic_Job_Key, Espace_Id, Tenant_Id, Executed_By, Error_Id, Should_Have_Run_At, Next_Run, Cycle, Request_Key, Espace_Name, Application_Name, Application_Key, Cyclic_Job_Name
    FROM dbo.oslog_Cyclic_Job_2');
