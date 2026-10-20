-- The screen log: one row per Traditional web screen request, written from the OutSystems 11 Log data reference
-- (view oslog_Screen); column names and types checked against the logging-database
-- survey [3.3].
--
-- oslog_Screen is a view over the current week's cycle table and
-- oslog_Screen_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- Columns that differ between the log views are returned under the standard
-- names of README.md (Standard columns).
-- Instant is when the request STARTED and the row is written when it ends:
-- give this log type its own lag (probes[].lag) above the longest request.
-- Duration is milliseconds. No Error_Id on this view, so every row is INFO;
-- errors arrive through the error log with the same Request_Key.
SELECT TOP (@limit)
    r.Instant,
    CONCAT(r.Screen_Type, N' request ', r.Screen) AS Message,
    r.Duration,
    r.Screen,
    r.Action_Name,
    r.Screen_Type,
    r.Access_Mode,
    r.Application_Name,
    r.Espace_Name,
    r.Request_Key,
    r.Session_Id,
    r.User_Id,
    r.Username,
    r.Tenant_Id,
    r.Client_IP,
    r.Executed_By AS Server,
    r.Session_Bytes,
    r.Viewstate_Bytes,
    r.Session_Requests,
    r.Msisdn
-- Each week's view is cut to @limit rows in Instant order before the
-- union, so a backlog costs @limit index rows per view instead of every row
-- in (@from, @to]: the first @limit rows overall are among them.
FROM (
    SELECT * FROM (
        SELECT TOP (@limit) * FROM dbo.oslog_Screen
        WHERE Instant > @from AND Instant <= @to
        ORDER BY Instant
    ) AS current_week
    UNION ALL
    SELECT * FROM (
        SELECT TOP (@limit) * FROM dbo.oslog_Screen_Previous
        WHERE Instant > @from AND Instant <= @to
        ORDER BY Instant
    ) AS previous_week
) AS r
ORDER BY r.Instant;
