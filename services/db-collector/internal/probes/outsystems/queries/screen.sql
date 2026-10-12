-- The screen log: one row per Traditional web screen request, written from the OutSystems 11 Log data reference
-- (view oslog_Screen); confirm the column names, types and units against
-- the real logging database before the probe review.
--
-- oslog_Screen is a view over the current week's cycle table and
-- oslog_Screen_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- Instant is when the request STARTED and the row is written when it ends:
-- give this log type its own lag (probes[].lag) above the longest request.
-- Duration is milliseconds. No Error_Id on this view, so every row is INFO;
-- errors arrive through the error log with the same Request_Key.
SELECT TOP (@limit)
    r.Instant,
    CONCAT(r.Screen_Type, N' request ', r.Screen) AS Message,
    r.Duration,
    r.Screen,
    r.Screen_Type,
    r.Access_Mode,
    r.Application_Name,
    r.Espace_Name,
    r.Action_Name,
    r.Request_Key,
    r.Session_Id,
    r.User_Id,
    r.Username,
    r.Tenant_Id,
    r.Client_IP,
    r.Executed_By,
    r.Session_Bytes,
    r.Viewstate_Bytes,
    r.Session_Requests,
    r.Msisdn
FROM (
    SELECT * FROM dbo.oslog_Screen
    WHERE Instant > @from AND Instant <= @to
    UNION ALL
    SELECT * FROM dbo.oslog_Screen_Previous
    WHERE Instant > @from AND Instant <= @to
) AS r
ORDER BY r.Instant;
