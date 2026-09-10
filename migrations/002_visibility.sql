-- Legacy manual ownership was never recorded. Preserve bytes/history but deny
-- user access, including mixed jobs that may already contain manual pollution.
UPDATE sources SET body=JSON_SET(body,'$.visibility',IF(JSON_UNQUOTE(JSON_EXTRACT(body,'$.trust'))='MANUAL','PRIVATE','GLOBAL'),'$.timezone','Asia/Shanghai') WHERE JSON_EXTRACT(body,'$.visibility') IS NULL OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.visibility'))='';
UPDATE jobs j SET visibility='PRIVATE',owner_id='',body=JSON_SET(body,'$.visibility','PRIVATE','$.owner_id','') WHERE EXISTS (SELECT 1 FROM postings p JOIN sources s ON s.id=p.source_id WHERE p.job_id=j.id AND JSON_UNQUOTE(JSON_EXTRACT(s.body,'$.visibility'))='PRIVATE');
