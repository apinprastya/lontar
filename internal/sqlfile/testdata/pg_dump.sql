--
-- PostgreSQL database dump
--

\restrict JLAgTgIbhjjcpHYlSAQvL9zlqyt1L8hS76CWOwPe4Ujq4TLL72QjEi2D8fd2AzP

-- Dumped from database version 16.16
-- Dumped by pg_dump version 16.16

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: dumpfx; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA dumpfx;


--
-- Name: bump(integer); Type: FUNCTION; Schema: dumpfx; Owner: -
--

CREATE FUNCTION dumpfx.bump(x integer) RETURNS integer
    LANGUAGE plpgsql
    AS $$ BEGIN RETURN x + 1; -- ; inside
END; $$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: child; Type: TABLE; Schema: dumpfx; Owner: -
--

CREATE TABLE dumpfx.child (
    id integer NOT NULL,
    item_id integer,
    label character varying(20)
);


--
-- Name: empty_t; Type: TABLE; Schema: dumpfx; Owner: -
--

CREATE TABLE dumpfx.empty_t (
    a integer
);


--
-- Name: items; Type: TABLE; Schema: dumpfx; Owner: -
--

CREATE TABLE dumpfx.items (
    id integer NOT NULL,
    name text NOT NULL,
    note text,
    data bytea,
    tags text[],
    meta jsonb,
    qty numeric(10,2)
);


--
-- Name: item_names; Type: VIEW; Schema: dumpfx; Owner: -
--

CREATE VIEW dumpfx.item_names AS
 SELECT id,
    upper(name) AS name
   FROM dumpfx.items;


--
-- Name: items_id_seq; Type: SEQUENCE; Schema: dumpfx; Owner: -
--

CREATE SEQUENCE dumpfx.items_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: items_id_seq; Type: SEQUENCE OWNED BY; Schema: dumpfx; Owner: -
--

ALTER SEQUENCE dumpfx.items_id_seq OWNED BY dumpfx.items.id;


--
-- Name: items id; Type: DEFAULT; Schema: dumpfx; Owner: -
--

ALTER TABLE ONLY dumpfx.items ALTER COLUMN id SET DEFAULT nextval('dumpfx.items_id_seq'::regclass);


--
-- Data for Name: child; Type: TABLE DATA; Schema: dumpfx; Owner: -
--

COPY dumpfx.child (id, item_id, label) FROM stdin;
1	1	one
2	3	three
\.


--
-- Data for Name: empty_t; Type: TABLE DATA; Schema: dumpfx; Owner: -
--

COPY dumpfx.empty_t (a) FROM stdin;
\.


--
-- Data for Name: items; Type: TABLE DATA; Schema: dumpfx; Owner: -
--

COPY dumpfx.items (id, name, note, data, tags, meta, qty) FROM stdin;
1	plain	\N	\\xdeadbeef	{a,b}	{"k": [1, 2.50, null]}	12.50
2	tab\there	two\nlines	\N	{}	\N	\N
3	back\\slash	\\.	\N	\N	{"s": "x;y"}	-1.00
4	ünïcødé 日本	\r\nCRLF inside	\\x00	{"with space","q\\"uote"}	[]	0.00
5	semi;colon	-- not a comment	\N	\N	\N	3.14
\.


--
-- Name: items_id_seq; Type: SEQUENCE SET; Schema: dumpfx; Owner: -
--

SELECT pg_catalog.setval('dumpfx.items_id_seq', 5, true);


--
-- Name: child child_pkey; Type: CONSTRAINT; Schema: dumpfx; Owner: -
--

ALTER TABLE ONLY dumpfx.child
    ADD CONSTRAINT child_pkey PRIMARY KEY (id);


--
-- Name: items items_pkey; Type: CONSTRAINT; Schema: dumpfx; Owner: -
--

ALTER TABLE ONLY dumpfx.items
    ADD CONSTRAINT items_pkey PRIMARY KEY (id);


--
-- Name: items_name; Type: INDEX; Schema: dumpfx; Owner: -
--

CREATE INDEX items_name ON dumpfx.items USING btree (name);


--
-- Name: child child_item_id_fkey; Type: FK CONSTRAINT; Schema: dumpfx; Owner: -
--

ALTER TABLE ONLY dumpfx.child
    ADD CONSTRAINT child_item_id_fkey FOREIGN KEY (item_id) REFERENCES dumpfx.items(id);


--
-- PostgreSQL database dump complete
--

\unrestrict JLAgTgIbhjjcpHYlSAQvL9zlqyt1L8hS76CWOwPe4Ujq4TLL72QjEi2D8fd2AzP

