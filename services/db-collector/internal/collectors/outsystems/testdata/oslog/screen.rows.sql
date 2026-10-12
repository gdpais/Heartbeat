-- Representative rows for the screen log type, loaded after screen.sql by
-- make test-sqlserver. The query must return every row inserted here, from
-- the previous week's table as well as the current week's, so there are rows
-- on both sides of the Friday 2025-09-26 23:45 rotation. The Request_Key
-- values are shared with the other log types' rows: one request appears
-- across screen, integration, extension, error and general.
--
-- Stand-in rows until rows copied from the logging database replace them
-- (TODO 3.3); copy them as they are, user ids, sessions and IPs included.
INSERT INTO dbo.oslog_Screen_2 (Instant, Duration, Screen, Session_Id, User_Id, Espace_Id, Tenant_Id, Msisdn, Screen_Type, Executed_By, Session_Bytes, Viewstate_Bytes, Session_Requests, Access_Mode, Request_Key, Action_Name, Client_IP, Espace_Name, Application_Name, Application_Key, Username) VALUES
    ('2025-09-26T23:41:11.515', 842, N'Checkout', 'u7k2v4zz5qd3m9n1p8r6s0t2', 1042, 17, 0, NULL, N'Web', N'FE-01', 18432, 65120, 14, N'Submit',
     'a1b2c3d4-0001-4a00-8000-000000000001', N'btnPlaceOrder_Click', N'10.20.30.41', N'Shop_CS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'maria.santos'),
    ('2025-09-26T23:42:03.004', 96, N'OrderList', 'u7k2v4zz5qd3m9n1p8r6s0t2', 1042, 17, 0, NULL, N'Web', N'FE-01', 18944, 12304, 15, N'Ajax',
     'a1b2c3d4-0007-4a00-8000-000000000007', N'RefreshList', N'10.20.30.41', N'Shop_CS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'maria.santos');
INSERT INTO dbo.oslog_Screen_3 (Instant, Duration, Screen, Session_Id, User_Id, Espace_Id, Tenant_Id, Msisdn, Screen_Type, Executed_By, Session_Bytes, Viewstate_Bytes, Session_Requests, Access_Mode, Request_Key, Action_Name, Client_IP, Espace_Name, Application_Name, Application_Key, Username) VALUES
    ('2025-09-27T00:03:04.813', 310, N'Login', 'w3x9y1z5a7b2c4d6e8f0g2h4', 2077, 23, 0, NULL, N'Web', N'FE-01', 2048, 3120, 1, N'Submit',
     'a1b2c3d4-0003-4a00-8000-000000000003', N'DoLogin', N'10.20.30.77', N'Users', N'Users', 'b1c2d3e4-f5a6-4b7c-9d8e-0f1a2b3c4d5e', N'joao.pereira');
