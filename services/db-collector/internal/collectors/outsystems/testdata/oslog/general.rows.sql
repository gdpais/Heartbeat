-- Representative rows for the general log type, loaded after general.sql by
-- make test-sqlserver. The query must return every row inserted here, from
-- the previous week's table as well as the current week's, so there are rows
-- on both sides of the Friday 2025-09-26 23:45 rotation. The Request_Key
-- values are shared with the other log types' rows: one request appears
-- across screen, integration, extension, error and general.
--
-- Stand-in rows until rows copied from the logging database replace them
-- (TODO 3.3); copy them as they are, user ids, sessions and IPs included.
INSERT INTO dbo.oslog_General_2 (Instant, Session_Id, User_Id, Espace_Id, Tenant_Id, Error_Id, Message, Message_Type, Module_Name, Request_Key, Action_Name, Entrypoint_Name, Client_IP, Espace_Name, Application_Name, Application_Key, Username) VALUES
    ('2025-09-26T23:41:11.900', 'u7k2v4zz5qd3m9n1p8r6s0t2', 1042, 17, 0, '', N'Order 10021 placed, 3 items, total 149.90', N'Orders', N'Shop_CS',
     'a1b2c3d4-0001-4a00-8000-000000000001', N'PlaceOrder', N'Checkout', N'10.20.30.41', N'Shop_CS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'maria.santos'),
    ('2025-09-26T23:44:58.990', NULL, NULL, 17, 0, 'e0000000-0000-4000-8000-000000000002', N'ProcessOrders: 240 orders pending, batch 1 of 3 timed out', N'Timers', N'Shop_CS',
     'a1b2c3d4-0002-4a00-8000-000000000002', N'ProcessOrders', N'ProcessOrders', NULL, N'Shop_CS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', NULL);
INSERT INTO dbo.oslog_General_3 (Instant, Session_Id, User_Id, Espace_Id, Tenant_Id, Error_Id, Message, Message_Type, Module_Name, Request_Key, Action_Name, Entrypoint_Name, Client_IP, Espace_Name, Application_Name, Application_Key, Username) VALUES
    ('2025-09-27T08:15:39.850', 'k5m7n9p1q3r5s7t9u1v3w5x7', 1042, 31, 0, '', N'Payment retry 1 for order 10022', N'Payments', N'Shop_IS',
     'a1b2c3d4-0004-4a00-8000-000000000004', N'Pay', N'Checkout', N'10.20.30.41', N'Shop_IS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'maria.santos'),
    ('2025-09-27T08:16:01.300', 'u7k2v4zz5qd3m9n1p8r6s0t2', 1042, 17, 0, '', N'SELECT o.Id, o.Total FROM OSUSR_abc_Order o WHERE o.CustomerId = @CustomerId ORDER BY o.CreatedOn DESC took 3456 ms', N'Info', N'SLOWSQL',
     'a1b2c3d4-0008-4a00-8000-000000000008', N'Orders_Get', N'Orders_Get', N'10.20.40.9', N'Shop_CS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'maria.santos');
