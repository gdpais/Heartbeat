-- The general log: messages apps write with LogMessage, written from the OutSystems 11 Log data reference
-- (view oslog_General); column names and types checked against the logging-database
-- survey [3.3].
--
-- oslog_General is a view over the current week's cycle table and
-- oslog_General_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- Columns that differ between the log views are returned under the standard
-- names of README.md (Standard columns).
-- No Severity column: every row is INFO. Module_Name is the free text the
-- app passed to LogMessage; the platform's own slow-query rows use SLOWSQL
-- and a Message of the form '<statement> took <n> ms', whose n becomes
-- Duration. An empty Error_Id is dropped (NULL).
SELECT TOP (@limit)
    r.Instant,
    r.Message,
    r.Message_Type,
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
    r.Client_IP,
    NULLIF(r.Error_Id, '') AS Error_Id,
    CASE WHEN r.Module_Name = 'SLOWSQL' AND r.Message LIKE '%took [0-9]% ms%'
         THEN TRY_CAST(SUBSTRING(r.Message, PATINDEX('%took [0-9]%', r.Message) + 5,
                                 CHARINDEX(' ms', r.Message, PATINDEX('%took [0-9]%', r.Message)) - PATINDEX('%took [0-9]%', r.Message) - 5) AS int)
    END AS Duration
-- Each week's view is cut to @limit rows in Instant order before the
-- union, so a backlog costs @limit index rows per view instead of every row
-- in (@from, @to]: the first @limit rows overall are among them.
FROM (
    SELECT * FROM (
        SELECT TOP (@limit) * FROM dbo.oslog_General
        WHERE Instant > @from AND Instant <= @to
        ORDER BY Instant
    ) AS current_week
    UNION ALL
    SELECT * FROM (
        SELECT TOP (@limit) * FROM dbo.oslog_General_Previous
        WHERE Instant > @from AND Instant <= @to
        ORDER BY Instant
    ) AS previous_week
) AS r
ORDER BY r.Instant;
