-- The request event log: custom events apps attach to a request, written from the OutSystems 11 Log data reference
-- (view oslog_RequestEvent); column names and types checked against the logging-database
-- survey [3.3].
--
-- oslog_RequestEvent is a view over the current week's cycle table and
-- oslog_RequestEvent_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- Columns that differ between the log views are returned under the standard
-- names of README.md (Standard columns).
-- This view names its columns in upper case without underscores; they are
-- aliased to the standard names. The event name is the operation
-- (Action_Name).
SELECT TOP (@limit)
    r.INSTANT AS Instant,
    CONCAT(r.REQUESTEVENTNAME, N': ', r.EVENTDETAILS) AS Message,
    r.REQUESTEVENTNAME AS Action_Name,
    r.EVENTDETAILS AS Event_Details,
    r.REQUESTKEY AS Request_Key,
    r.APPLICATIONNAME AS Application_Name,
    r.APPLICATIONKEY AS Application_Key,
    r.MODULENAME AS Espace_Name,
    r.MODULEKEY AS Module_Key
-- Each week's view is cut to @limit rows in Instant order before the
-- union, so a backlog costs @limit index rows per view instead of every row
-- in (@from, @to]: the first @limit rows overall are among them.
FROM (
    SELECT * FROM (
        SELECT TOP (@limit) * FROM dbo.oslog_RequestEvent
        WHERE INSTANT > @from AND INSTANT <= @to
        ORDER BY INSTANT
    ) AS current_week
    UNION ALL
    SELECT * FROM (
        SELECT TOP (@limit) * FROM dbo.oslog_RequestEvent_Previous
        WHERE INSTANT > @from AND INSTANT <= @to
        ORDER BY INSTANT
    ) AS previous_week
) AS r
ORDER BY r.INSTANT;
