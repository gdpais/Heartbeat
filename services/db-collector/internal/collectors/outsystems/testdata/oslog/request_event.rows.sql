-- Representative rows for the request_event log type, loaded after request_event.sql by
-- make test-sqlserver. The query must return every row inserted here, from
-- the previous week's table as well as the current week's, so there are rows
-- on both sides of the Friday 2025-09-26 23:45 rotation. The Request_Key
-- values are shared with the other log types' rows: one request appears
-- across screen, integration, extension, error and general.
--
-- Synthetic rows in the surveyed schema [3.3]: rows from a real logging
-- database never reach this public repository.
INSERT INTO dbo.oslog_RequestEvent_2 (INSTANT, REQUESTKEY, REQUESTEVENTNAME, MODULEKEY, MODULENAME, APPLICATIONKEY, APPLICATIONNAME, EVENTDETAILS) VALUES
    ('2025-09-26T23:41:12.100', 'a1b2c3d4-0001-4a00-8000-000000000001', N'OrderPlaced', 'd1e2f3a4-b5c6-4d7e-8f9a-0b1c2d3e4f5a', N'Shop_CS', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'Shop', N'{"order":10021,"items":3,"total":149.90}');
INSERT INTO dbo.oslog_RequestEvent_3 (INSTANT, REQUESTKEY, REQUESTEVENTNAME, MODULEKEY, MODULENAME, APPLICATIONKEY, APPLICATIONNAME, EVENTDETAILS) VALUES
    ('2025-09-27T08:15:40.020', 'a1b2c3d4-0004-4a00-8000-000000000004', N'PaymentFailed', 'd1e2f3a4-b5c6-4d7e-8f9a-0b1c2d3e4f5b', N'Shop_IS', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'Shop', N'{"order":10022,"provider":"payments","status":503}');
