-- The mobile request log: Reactive and Mobile server requests, written from the OutSystems 11 Log data reference
-- (view oslog_mobile_request); column names and types checked against the logging-database
-- survey [3.3].
--
-- oslog_mobile_request is a view over the current week's cycle table and
-- oslog_mobile_request_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- Columns that differ between the log views are returned under the standard
-- names of README.md (Standard columns).
-- Instant is when the request COMPLETED. Endpoint is the server action or
-- data action that ran (standard name Action_Name), Login_Id the session
-- (Session_Id) and Source the caller (Client_IP). The reference documents
-- Duration in SECONDS and Error_Id as an integer (0 for none); the surveyed
-- database stores Error_Id as text, like the other logs (empty for none).
-- Duration is converted to milliseconds here: the survey had no mobile
-- request rows to confirm the unit, so if the column holds milliseconds,
-- drop the * 1000.
SELECT TOP (@limit)
    r.Instant,
    CONCAT(r.Screen, N' ', r.Endpoint) AS Message,
    CASE WHEN NULLIF(r.Error_Id, '') IS NULL THEN 'INFO' ELSE 'ERROR' END AS Severity,
    r.Duration * 1000 AS Duration,
    r.Screen,
    r.Endpoint AS Action_Name,
    r.Application_Name,
    r.Espace_Name,
    r.Request_Key,
    r.Login_Id AS Session_Id,
    r.User_Id,
    r.Username,
    r.Tenant_Id,
    NULLIF(r.Source, '') AS Client_IP,
    r.Executed_By AS Server,
    NULLIF(r.Error_Id, '') AS Error_Id
-- Each week's view is cut to @limit rows in Instant order before the
-- union, so a backlog costs @limit index rows per view instead of every row
-- in (@from, @to]: the first @limit rows overall are among them.
FROM (
    SELECT * FROM (
        SELECT TOP (@limit) * FROM dbo.oslog_mobile_request
        WHERE Instant > @from AND Instant <= @to
        ORDER BY Instant
    ) AS current_week
    UNION ALL
    SELECT * FROM (
        SELECT TOP (@limit) * FROM dbo.oslog_mobile_request_Previous
        WHERE Instant > @from AND Instant <= @to
        ORDER BY Instant
    ) AS previous_week
) AS r
ORDER BY r.Instant;
