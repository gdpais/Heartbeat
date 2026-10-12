-- The mobile request log: Reactive and Mobile server requests, written from the OutSystems 11 Log data reference
-- (view oslog_mobile_request); confirm the column names, types and units against
-- the real logging database before the probe review.
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
-- Duration in SECONDS and Error_Id as an integer (0 for none); Duration is
-- converted to milliseconds here. If the real column holds milliseconds,
-- drop the * 1000.
SELECT TOP (@limit)
    r.Instant,
    CONCAT(r.Screen, N' ', r.Endpoint) AS Message,
    CASE WHEN NULLIF(r.Error_Id, 0) IS NULL THEN 'INFO' ELSE 'ERROR' END AS Severity,
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
    NULLIF(r.Error_Id, 0) AS Error_Id
FROM (
    SELECT * FROM dbo.oslog_mobile_request
    WHERE Instant > @from AND Instant <= @to
    UNION ALL
    SELECT * FROM dbo.oslog_mobile_request_Previous
    WHERE Instant > @from AND Instant <= @to
) AS r
ORDER BY r.Instant;
