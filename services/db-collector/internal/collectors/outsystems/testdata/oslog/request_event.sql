-- SQL Server fixture for the request event log type: the oslog objects
-- queries/request_event.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; request_event.rows.sql then fills them. PatientZero builds its logging
-- schema from this file too (tools/patientzero/internal/logschema).
--
-- The OutSystems 11 DDL from the logging-database survey [3.3]: the logging
-- database keeps 10 cycle tables per log type, oslog_RequestEvent_0 to
-- oslog_RequestEvent_9, one week each, behind two views, oslog_RequestEvent (the
-- current week) and oslog_RequestEvent_Previous (the week before). Two tables
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
CREATE TABLE dbo.oslog_RequestEvent_2 (
    INSTANT datetime2(3) NOT NULL,
    REQUESTKEY varchar(36) NOT NULL,
    REQUESTEVENTNAME varchar(50) NOT NULL,
    MODULEKEY varchar(36) NOT NULL,
    MODULENAME varchar(50) NOT NULL,
    APPLICATIONKEY varchar(36) NOT NULL,
    APPLICATIONNAME varchar(50) NOT NULL,
    EVENTDETAILS varchar(2000) NOT NULL,
    CYCLE int NOT NULL DEFAULT 2
);
CREATE NONCLUSTERED INDEX idx_oslog_RequestEvent_2 ON dbo.oslog_RequestEvent_2 (INSTANT, REQUESTEVENTNAME, APPLICATIONKEY);
CREATE CLUSTERED INDEX idx_oslog_RequestEvent_2_Instant ON dbo.oslog_RequestEvent_2 (INSTANT);
CREATE TABLE dbo.oslog_RequestEvent_3 (
    INSTANT datetime2(3) NOT NULL,
    REQUESTKEY varchar(36) NOT NULL,
    REQUESTEVENTNAME varchar(50) NOT NULL,
    MODULEKEY varchar(36) NOT NULL,
    MODULENAME varchar(50) NOT NULL,
    APPLICATIONKEY varchar(36) NOT NULL,
    APPLICATIONNAME varchar(50) NOT NULL,
    EVENTDETAILS varchar(2000) NOT NULL,
    CYCLE int NOT NULL DEFAULT 3
);
CREATE NONCLUSTERED INDEX idx_oslog_RequestEvent_3 ON dbo.oslog_RequestEvent_3 (INSTANT, REQUESTEVENTNAME, APPLICATIONKEY);
CREATE CLUSTERED INDEX idx_oslog_RequestEvent_3_Instant ON dbo.oslog_RequestEvent_3 (INSTANT);
EXEC (N'CREATE VIEW dbo.oslog_RequestEvent AS
    SELECT INSTANT, REQUESTKEY, REQUESTEVENTNAME, MODULEKEY, MODULENAME, APPLICATIONKEY, APPLICATIONNAME, EVENTDETAILS, CYCLE
    FROM dbo.oslog_RequestEvent_3');
EXEC (N'CREATE VIEW dbo.oslog_RequestEvent_Previous AS
    SELECT INSTANT, REQUESTKEY, REQUESTEVENTNAME, MODULEKEY, MODULENAME, APPLICATIONKEY, APPLICATIONNAME, EVENTDETAILS, CYCLE
    FROM dbo.oslog_RequestEvent_2');
