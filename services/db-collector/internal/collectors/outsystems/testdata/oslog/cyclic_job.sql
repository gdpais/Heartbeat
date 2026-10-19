-- SQL Server fixture for the cyclic job log type: the oslog objects
-- queries/cyclic_job.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; cyclic_job.rows.sql then fills them. PatientZero builds its logging
-- schema from this file too (tools/patientzero/internal/logschema).
--
-- The OutSystems 11 DDL from the logging-database survey [3.3]: the logging
-- database keeps 10 cycle tables per log type, oslog_Cyclic_Job_0 to
-- oslog_Cyclic_Job_9, one week each, behind two views, oslog_Cyclic_Job (the
-- current week) and oslog_Cyclic_Job_Previous (the week before). Two tables
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
CREATE TABLE dbo.oslog_Cyclic_Job_2 (
    Instant datetime2(3) NOT NULL,
    Duration int NULL,
    Cyclic_Job_Key varchar(50) NULL,
    Espace_Id int NOT NULL,
    Tenant_Id int NOT NULL,
    Executed_By varchar(50) NULL,
    Error_Id varchar(50) NULL,
    Should_Have_Run_At datetime2(3) NOT NULL,
    Next_Run datetime2(3) NOT NULL,
    Cycle int NOT NULL DEFAULT 2,
    Request_Key varchar(36) NULL,
    Espace_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    Cyclic_Job_Name varchar(50) NULL
);
CREATE NONCLUSTERED INDEX idx_oslog_Cyclic_Job_2_espace_id ON dbo.oslog_Cyclic_Job_2 (Instant, Espace_Id);
CREATE TABLE dbo.oslog_Cyclic_Job_3 (
    Instant datetime2(3) NOT NULL,
    Duration int NULL,
    Cyclic_Job_Key varchar(50) NULL,
    Espace_Id int NOT NULL,
    Tenant_Id int NOT NULL,
    Executed_By varchar(50) NULL,
    Error_Id varchar(50) NULL,
    Should_Have_Run_At datetime2(3) NOT NULL,
    Next_Run datetime2(3) NOT NULL,
    Cycle int NOT NULL DEFAULT 3,
    Request_Key varchar(36) NULL,
    Espace_Name varchar(50) NULL,
    Application_Name varchar(50) NULL,
    Application_Key varchar(100) NULL,
    Cyclic_Job_Name varchar(50) NULL
);
CREATE NONCLUSTERED INDEX idx_oslog_Cyclic_Job_3_espace_id ON dbo.oslog_Cyclic_Job_3 (Instant, Espace_Id);
EXEC (N'CREATE VIEW dbo.oslog_Cyclic_Job AS
    SELECT Instant, Duration, Cyclic_Job_Key, Espace_Id, Tenant_Id, Executed_By, Error_Id, Should_Have_Run_At, Next_Run, Cycle, Request_Key, Espace_Name, Application_Name, Application_Key, Cyclic_Job_Name
    FROM dbo.oslog_Cyclic_Job_3');
EXEC (N'CREATE VIEW dbo.oslog_Cyclic_Job_Previous AS
    SELECT Instant, Duration, Cyclic_Job_Key, Espace_Id, Tenant_Id, Executed_By, Error_Id, Should_Have_Run_At, Next_Run, Cycle, Request_Key, Espace_Name, Application_Name, Application_Key, Cyclic_Job_Name
    FROM dbo.oslog_Cyclic_Job_2');
