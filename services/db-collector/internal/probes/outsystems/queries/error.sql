-- The error log: one row per logged exception, written from the OutSystems 11 Log data reference
-- (view oslog_Error); column names and types checked against the logging-database
-- survey [3.3].
--
-- oslog_Error is a view over the current week's cycle table and
-- oslog_Error_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- Columns that differ between the log views are returned under the standard
-- names of README.md (Standard columns).
-- Entrypoint_Name is the entry point (standard name Screen). Module_Name is
-- the free text the error was logged with, Espace_Name the module.
SELECT TOP (@limit)
    r.Instant,
    r.Message,
    'ERROR' AS Severity,
    r.Application_Name,
    r.Espace_Name,
    r.Module_Name,
    r.Entrypoint_Name AS Screen,
    r.Action_Name,
    r.Request_Key,
    r.Session_Id,
    r.User_Id,
    r.Username,
    r.Tenant_Id,
    r.Server,
    r.Stack,
    r.EnvironmentInformation
FROM (
    SELECT * FROM dbo.oslog_Error
    WHERE Instant > @from AND Instant <= @to
    UNION ALL
    SELECT * FROM dbo.oslog_Error_Previous
    WHERE Instant > @from AND Instant <= @to
) AS r
ORDER BY r.Instant;
