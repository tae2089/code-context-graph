-- PostgreSQL rejects this downgrade instead of truncating annotation content
-- when existing values exceed the historical limits.
ALTER TABLE annotations
    ALTER COLUMN summary TYPE varchar(1024),
    ALTER COLUMN context TYPE varchar(2048);
