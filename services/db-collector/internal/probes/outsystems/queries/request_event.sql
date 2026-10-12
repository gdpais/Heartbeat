-- The request event log: custom events apps attach to a request, written from the OutSystems 11 Log data reference
-- (view oslog_RequestEvent); confirm the column names, types and units against
-- the real logging database before the probe review.
--
-- oslog_RequestEvent is a view over the current week's cycle table and
-- oslog_RequestEvent_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- This view names its columns in upper case without underscores; they are
-- aliased to the names the other log types use.
SELECT TOP (@limit)
    r.INSTANT AS Instant,
    CONCAT(r.REQUESTEVENTNAME, N': ', r.EVENTDETAILS) AS Message,
    r.REQUESTEVENTNAME AS Request_Event_Name,
    r.REQUESTKEY AS Request_Key,
    r.APPLICATIONNAME AS Application_Name,
    r.APPLICATIONKEY AS Application_Key,
    r.MODULENAME AS Module_Name,
    r.MODULEKEY AS Module_Key
FROM (
    SELECT * FROM dbo.oslog_RequestEvent
    WHERE INSTANT > @from AND INSTANT <= @to
    UNION ALL
    SELECT * FROM dbo.oslog_RequestEvent_Previous
    WHERE INSTANT > @from AND INSTANT <= @to
) AS r
ORDER BY r.INSTANT;
