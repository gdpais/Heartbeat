-- SQL Server fixture for the request_event log type: the oslog objects
-- queries/request_event.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table and view, nothing
-- else; request_event.rows.sql then fills them.
--
-- A stand-in shaped like OutSystems 11 (TODO 3.3, SQL Server fixture): the
-- logging database keeps 10 cycle tables per log type, oslog_RequestEvent_0 to
-- oslog_RequestEvent_9, one week each, behind two views, oslog_RequestEvent (the current
-- week) and oslog_RequestEvent_Previous (the week before). Two tables and the two
-- views are enough for the tests. The columns and their order are the
-- documented fields of this log (OutSystems 11 Log data reference); the SQL
-- Server types are guesses, INSTANT is datetime. Replace with the real DDL
-- of one cycle table and the two views.
--
-- CREATE VIEW must be alone in its batch and the tests run this file as one
-- batch, so the views are created through EXEC.
CREATE TABLE dbo.oslog_RequestEvent_2 (
    INSTANT datetime NOT NULL,
    REQUESTKEY char(36) NULL,
    REQUESTEVENTNAME nvarchar(100) NULL,
    MODULEKEY char(36) NULL,
    MODULENAME nvarchar(50) NULL,
    APPLICATIONKEY char(36) NULL,
    APPLICATIONNAME nvarchar(100) NULL,
    EVENTDETAILS nvarchar(max) NULL,
    CYCLE int NOT NULL DEFAULT 2
);
CREATE CLUSTERED INDEX IX_oslog_RequestEvent_2_INSTANT ON dbo.oslog_RequestEvent_2 (INSTANT);
CREATE TABLE dbo.oslog_RequestEvent_3 (
    INSTANT datetime NOT NULL,
    REQUESTKEY char(36) NULL,
    REQUESTEVENTNAME nvarchar(100) NULL,
    MODULEKEY char(36) NULL,
    MODULENAME nvarchar(50) NULL,
    APPLICATIONKEY char(36) NULL,
    APPLICATIONNAME nvarchar(100) NULL,
    EVENTDETAILS nvarchar(max) NULL,
    CYCLE int NOT NULL DEFAULT 3
);
CREATE CLUSTERED INDEX IX_oslog_RequestEvent_3_INSTANT ON dbo.oslog_RequestEvent_3 (INSTANT);
EXEC (N'CREATE VIEW dbo.oslog_RequestEvent AS
    SELECT INSTANT, REQUESTKEY, REQUESTEVENTNAME, MODULEKEY, MODULENAME, APPLICATIONKEY, APPLICATIONNAME, EVENTDETAILS, CYCLE
    FROM dbo.oslog_RequestEvent_3');
EXEC (N'CREATE VIEW dbo.oslog_RequestEvent_Previous AS
    SELECT INSTANT, REQUESTKEY, REQUESTEVENTNAME, MODULEKEY, MODULENAME, APPLICATIONKEY, APPLICATIONNAME, EVENTDETAILS, CYCLE
    FROM dbo.oslog_RequestEvent_2');
