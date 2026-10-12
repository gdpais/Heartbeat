-- Representative rows for the integration log type, loaded after integration.sql by
-- make test-sqlserver. The query must return every row inserted here, from
-- the previous week's table as well as the current week's, so there are rows
-- on both sides of the Friday 2025-09-26 23:45 rotation. The Request_Key
-- values are shared with the other log types' rows: one request appears
-- across screen, integration, extension, error and general.
--
-- Stand-in rows until rows copied from the logging database replace them
-- (TODO 3.3); copy them as they are, user ids, sessions and IPs included.
INSERT INTO dbo.oslog_Integration_2 (Id, Instant, Duration, Source, Endpoint, Action, Type, Espace_Id, Tenant_Id, Error_Id, Executed_By, Is_Expose, Request_Key, Espace_Name, Application_Name, Application_Key) VALUES
    ('i0000000-0000-4000-8000-000000000001', '2025-09-26T23:41:11.830', 312, N'', N'https://rates.example.internal/api/v1/rates', N'GetRates', N'REST', 17, 0, '', N'FE-01', 0,
     'a1b2c3d4-0001-4a00-8000-000000000001', N'Shop_CS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d');
INSERT INTO dbo.oslog_Integration_3 (Id, Instant, Duration, Source, Endpoint, Action, Type, Espace_Id, Tenant_Id, Error_Id, Executed_By, Is_Expose, Request_Key, Espace_Name, Application_Name, Application_Key) VALUES
    ('i0000000-0000-4000-8000-000000000002', '2025-09-27T08:15:39.998', 30012, N'', N'https://payments.example.internal/charge', N'Charge', N'REST', 31, 0, 'e0000000-0000-4000-8000-000000000004', N'FE-02', 0,
     'a1b2c3d4-0004-4a00-8000-000000000004', N'Shop_IS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d'),
    ('i0000000-0000-4000-8000-000000000003', '2025-09-27T08:16:02.120', 45, N'10.20.40.9', N'', N'Orders_Get', N'REST', 17, 0, '', N'FE-01', 1,
     'a1b2c3d4-0008-4a00-8000-000000000008', N'Shop_CS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d');
