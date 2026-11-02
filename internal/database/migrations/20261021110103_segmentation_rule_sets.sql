-- +goose Up
-- Segmentation rule sets, per source language: the built-ins OpenLoc ships,
-- and each org's own. Both take their ids from one sequence, so an id names
-- one set across the two tables and segmentation_rule_set_catalog lists them
-- together.
CREATE SEQUENCE segmentation_rule_set_id_seq;

-- The built-ins: read-only, the same for every org, and never changed once
-- released (an improvement is a new row with the next version in
-- builtin_key). An org copies one to change it. No tenant, and nothing
-- references a tenant from here.
CREATE TABLE segmentation_builtin_rule_sets (
    id bigint PRIMARY KEY DEFAULT nextval('segmentation_rule_set_id_seq'),
    -- "builtin:<name>@<version>".
    builtin_key text NOT NULL UNIQUE,
    name text NOT NULL,
    -- A normalized language code, or empty for a set that suits any language.
    source_language text NOT NULL,
    description text NOT NULL,
    definition jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- An org's own sets. One live set per language may be the Default, which
-- new uploads in that language are segmented with. definition is the full
-- rule set (segmentation.RuleSet). Soft-deleted: live reads filter
-- deleted_at IS NULL.
CREATE TABLE segmentation_rule_sets (
    id bigint PRIMARY KEY DEFAULT nextval('segmentation_rule_set_id_seq'),
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    -- A normalized language code: "fr" or "fr-ca".
    source_language text NOT NULL CHECK (source_language <> ''),
    is_default boolean NOT NULL DEFAULT false,
    definition jsonb NOT NULL,
    -- The set this one was copied from (a built-in or an org set), for
    -- display only.
    based_on bigint,
    created_by bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE UNIQUE INDEX segmentation_rule_sets_default_idx
    ON segmentation_rule_sets (tenant_id, source_language)
    WHERE is_default AND deleted_at IS NULL;
CREATE UNIQUE INDEX segmentation_rule_sets_name_idx
    ON segmentation_rule_sets (tenant_id, lower(name))
    WHERE deleted_at IS NULL;

-- Both kinds in one shape for reads; tenant_id is NULL on a built-in.
CREATE VIEW segmentation_rule_set_catalog AS
SELECT id, NULL::bigint AS tenant_id, builtin_key, name, source_language, description,
       false AS is_default, definition, NULL::bigint AS based_on,
       created_at, created_at AS updated_at, NULL::timestamptz AS deleted_at
FROM segmentation_builtin_rule_sets
UNION ALL
SELECT id, tenant_id, NULL::text, name, source_language, ''::text,
       is_default, definition, based_on,
       created_at, updated_at, deleted_at
FROM segmentation_rule_sets;

-- The built-ins, as internal/segmentation/builtin/*.json holds them
-- (TestIntegrationBuiltinRowsMatchTheCatalog keeps the two equal).
INSERT INTO segmentation_builtin_rule_sets (builtin_key, name, source_language, description, definition) VALUES
    ('builtin:default@1', 'Any language', '', 'Sentence-ending punctuation of many scripts. Full-width 。！？ end a sentence without a following space.',
     $def${"mode":"sentence","rules":[{"before":"(?:^|\\P{L})\\p{Ll}\\.","after":"","break":false},{"before":"[.!?…][\\p{Pe}\\p{Pf}\"']*","after":"[\\t\\n\\v\\f\\r\\x{85}\\p{Z}]+[\\p{Lu}\\p{Lt}\\p{Lo}\\p{Nd}\\p{Ps}\\p{Pi}\"'¿¡]","break":true},{"before":"[。！？｡]","after":"[^\\p{Pe}\\p{Pf}。！？｡]","break":true},{"before":"[؟۔।॥][\\p{Pe}\\p{Pf}\"']*","after":"[\\t\\n\\v\\f\\r\\x{85}\\p{Z}]+\\S","break":true}],"abbreviations":[],"number_abbreviations":[]}$def$),
    ('builtin:conservative@1', 'Conservative (technical)', '', 'Breaks only after . ! ? or : followed by a space and a capital letter, digit or opening bracket or quote. Few, long segments; suits technical documentation.',
     $def${"mode":"sentence","rules":[{"before":"(?:^|\\P{L})\\p{Ll}\\.","after":"","break":false},{"before":"(?:^|\\P{L})(?:[Ee][Tt][Cc]|[Ff][Iiİ][Gg]|[Ff][Iiİ][Gg][Ss]|[Aa][Pp][Pp][Rr][Oo][Xx]|[Vv][Ss]|[Cc][Ff]|[Dd][Rr]|[Mm][Rr]|[Mm][Rr][Ss]|[Pp][Rr][Oo][Ff]|[Iiİ][Nn][Cc]|[Ll][Tt][Dd]|[Cc][Hh][Aa][Pp])\\.","after":"","break":false},{"before":"[.!?:]","after":"[\\t\\n\\v\\f\\r\\x{85}\\p{Z}]+[\\p{Lu}\\p{Nd}(\\[{\"'«“‘]","break":true}],"abbreviations":[],"number_abbreviations":[]}$def$),
    ('builtin:de@1', 'German', 'de', 'German abbreviations (Unicode CLDR, curated) and sentence-ending punctuation.',
     $def${"mode":"sentence","rules":[{"before":"(?:^|\\P{L})\\p{Ll}\\.","after":"","break":false},{"before":"(?:^|[^\\p{L}\\p{N}])\\p{Nd}{1,3}\\.","after":"[\\t\\n\\v\\f\\r\\x{85}\\p{Z}]+\\p{L}","break":false},{"before":"[.!?…][\\p{Pe}\\p{Pf}\"']*","after":"[\\t\\n\\v\\f\\r\\x{85}\\p{Z}]+[\\p{Lu}\\p{Lt}\\p{Lo}\\p{Nd}\\p{Ps}\\p{Pi}\"'¿¡]","break":true},{"before":"[。！？｡]","after":"[^\\p{Pe}\\p{Pf}。！？｡]","break":true}],"abbreviations":["Alt","Di","Ges","frz","entspr","Gebr","erw","Frl","Inh","k.u.k","Ca","J.D","Ausg","evtl","So","i.B","s.a","kgl","Sept","o.B","Sa","ev","Dez","am","i.R","eigtl","i.J","u.U","G","z.Hd","u.A.w.g","Kl","Spezif","Obj","Ing","D. h","Folg","i.A","Msp","U.U","Chr","R","Einh","schwäb","Vgl","Aug","Dipl.-Ing","W","B","U. U","J","Fa","Mo","n.u.Z","Op","Mrd","e.h","Hr","Hrn","Ztr","k. u. k","Bibl","d.Ä","b","M","i.H","v.R.w","o.A","St","Dr","Fn","Abs","Rd","Dtzd","Jahrh","Z","Std","n. Chr","möbl","tägl","gest","gesch","z.B","Hbf","Abt","A.M","e.Wz","v.T","Nov","z","Prot","U.S","Wg","u.v.a","Adr","ggf","ggfs","Jan","O","Rel","od","Pfd","a.a.O","p.Adr","P","Gem","v. Chr","z.Z","S.A","i.V","verh","Ausschl","m.W","Dir","Verf","Sek","r","Chin","Feb","Int","Sep","Gesch","schweiz","Bed","a.Rh","jew","vgl","a.M","Str","exkl","gek","Erf","u.Ä","ehem","näml","u. Z","v. u. Z","sog","C","Dipl.-Kfm","mtl","Hrsg","Qu","röm","u","U","Adj","Kap","hpts","a.D","gedr","Best","N","v.u.Z","Phys","Fr","d.J","Reg.-Bez","m.E","schles","Ltd","südd","inkl","geb","Ggf","Inc","kath","kfm","Nr","Proz","Dim","verw","Reg","Dat","Evtl","led","F","Schr","Do","PIN","Z. Zt","v.Chr","Tägl","s","amtl","Temp","Mind","e.V","Abw","P.M","F.f","a.a.S","Mod","Co","Min","Allg","Geograph","Jr","Urspr","Apr","Z. B","v.H","A","einschl","Trans","zzgl","StR","Fam","I","jhrl","u.a","Ben","o.g","Kfm","Konv","Mi","L","beil","T","Ursprüngl","röm.-kath","Okt","u.ä","Tel","D","Ber","Kop","Mio","Y","U.S.A","v. H","Forts. f","Rep","Hptst","österr"],"number_abbreviations":["Nr","Abb","Art","Bd","Kap","Tab","Abschn"]}$def$),
    ('builtin:en@1', 'English', 'en', 'English abbreviations (Unicode CLDR, curated) and sentence-ending punctuation.',
     $def${"mode":"sentence","rules":[{"before":"(?:^|\\P{L})\\p{Ll}\\.","after":"","break":false},{"before":"[.!?…][\\p{Pe}\\p{Pf}\"']*","after":"[\\t\\n\\v\\f\\r\\x{85}\\p{Z}]+[\\p{Lu}\\p{Lt}\\p{Lo}\\p{Nd}\\p{Ps}\\p{Pi}\"'¿¡]","break":true},{"before":"[。！？｡]","after":"[^\\p{Pe}\\p{Pf}。！？｡]","break":true}],"abbreviations":["L.P","Alt","Approx","E.G","O","Maj","Misc","P.O","J.D","Jam","Dec","Sept","MR","G","D.C","M.T","Hz","Mrs","Var","N.V","Aug","B","S.A","Num","M.I.T","Org","Cont","U","Mart","Fn","Abs","Lt","Z","E","Kb","Est","A.M","L.A","Prof","U.S","Nov","Ph.D","Mar","I.T","exec","Jan","N.Y","X","Md","vs","D.A","A.D","R.L","P.M","M.R","Cap","Feb","Exec","I.e","Sep","Gb","K","U.S.C","Mt","S","A.S","C.O.D","Capt","Col","C.F","Adj","I.D","Mgr","R.T","B.V","M","Conn","Yr","Rev","Phys","pp","Ms","Sgt","J.K","Nr","Jun","Fri","S.A.R","Lev","Lt.Cdr","F","Id","Mr","Dept","Pvt","Hon.B.A","Q","Mb","Min","J.B","S.p.A","a.m","Comm","VS","L","PP","P.V","T","K.R","Etc","D","Adv","E.g","U.S.A","S.E","Rep","Sq","e.g","i.e","etc","cf","al","approx","esp","incl","misc","viz","ca","resp","dept","Dr","Jr","Sr","Inc","Ltd","Co","Corp","Bros","Gen","Gov","Sen","St","Apr","Jul","Oct","Mon","Tue","Tues","Wed","Thu","Thur","Thurs","Sat"],"number_abbreviations":["No","no","Nos","nos","nr","Fig","fig","Figs","figs","Vol","vol","Ch","ch","Sec","sec","Art","art","Eq","eq","Ref","ref","Tab","tab","Para","para","Pt","pt","Op","op"]}$def$),
    ('builtin:es@1', 'Spanish', 'es', 'Spanish abbreviations (Unicode CLDR, curated) and sentence-ending punctuation.',
     $def${"mode":"sentence","rules":[{"before":"(?:^|\\P{L})\\p{Ll}\\.","after":"","break":false},{"before":"[.!?…][\\p{Pe}\\p{Pf}\"']*","after":"[\\t\\n\\v\\f\\r\\x{85}\\p{Z}]+[\\p{Lu}\\p{Lt}\\p{Lo}\\p{Nd}\\p{Ps}\\p{Pi}\"'¿¡]","break":true},{"before":"[。！？｡]","after":"[^\\p{Pe}\\p{Pf}。！？｡]","break":true}],"abbreviations":["Rdos","JJ.OO","Sres","fig","may","RR.HH","oct","cap","mié","doc","Excmo","Trab","Excmos","Inc","FF.CC","DC","ago","trad","SA","Rvdos","ed","Exmo","jul","col","Srtas","ene","Rol","Fabric","Comm","vid","Da","dic","ss","abr","ntra","Sra","dtor","cf","dom","prov","Emm","Sr","licdo","p.ej","bol","figs","Vda","Dr","ntro","Desv","O.M","Ldo","Drs","sáb","feb","Ltda","Lcda","Exma","C.V","SS.MM","Lda","U.S","hnos","R.D","Korn","v.gr","vs","Ilmas","Rdo","ej","vie","jue","a. C","Ilmos","e. c","Excma","afma","licda","Em","K","sras","MM","fund","Mons","Lcdo","afmo","C","A.C","dptos","Col","Srta","Av","Ant","depto","H.P","D","M","C.P","Rev","Rvdmos","Fr","Ilmo","afmos","Ltd","afmas","prof","lun","SS.AA","nov","mss","Dña","Seg","Rvdmo","Reg","ms","Sras","sres","U.S.A","Sta","Sdad","Dra","srs","R.U","deptos","dpto","jun","bco","Cía","Id","Mr","e.g","C.S","Excmas","Dª","Rvdo","Lic","cfr","Corp","Dto","Ilma","L","PP","d. C","Ltdo","mtro","Mrs","Desc","Avda","Exmas","a. e. c","Exmos","AA","Sto","CA","sept","Exc","c/c"],"number_abbreviations":["núm","Núm","pág","Pág","cap","Cap","art","Art","vol","Vol"]}$def$),
    ('builtin:fr@1', 'French', 'fr', 'French abbreviations (Unicode CLDR, curated) and sentence-ending punctuation.',
     $def${"mode":"sentence","rules":[{"before":"(?:^|\\P{L})\\p{Ll}\\.","after":"","break":false},{"before":"[.!?…][\\p{Pe}\\p{Pf}\"']*","after":"[\\t\\n\\v\\f\\r\\x{85}\\p{Z}]+[\\p{Lu}\\p{Lt}\\p{Lo}\\p{Nd}\\p{Ps}\\p{Pi}\"'¿¡]","break":true},{"before":"[。！？｡]","after":"[^\\p{Pe}\\p{Pf}。！？｡]","break":true}],"abbreviations":["aux","config","collab","M","dim","imprim","oct","syst","bull","MM","doc","P.O","hôp","juil","broch","adr","symb","C","anc","voit","Jr","graph","dir","éd","fig","édit","niv","quart","cam","éval","anon","réf","Prof","févr","indus","DC","équiv","illustr","acoust","nov","L","U.S","S.M.A.R.T","sept","avr","dest","P.-D. G","ill","coll","encycl","ven","P","lun","Inc","sam","D","append","categ","janv","S.A","imm","U.S.A","mar","exempl","déc","ann","U","synth","dict","av. J.-C","W","Op","ap. J.-C","gouv","trav. publ"],"number_abbreviations":["no","No","p","pp","art","Art","vol","Vol","chap","Chap"]}$def$),
    ('builtin:it@1', 'Italian', 'it', 'Italian abbreviations (Unicode CLDR, curated) and sentence-ending punctuation.',
     $def${"mode":"sentence","rules":[{"before":"(?:^|\\P{L})\\p{Ll}\\.","after":"","break":false},{"before":"[.!?…][\\p{Pe}\\p{Pf}\"']*","after":"[\\t\\n\\v\\f\\r\\x{85}\\p{Z}]+[\\p{Lu}\\p{Lt}\\p{Lo}\\p{Nd}\\p{Ps}\\p{Pi}\"'¿¡]","break":true},{"before":"[。！？｡]","after":"[^\\p{Pe}\\p{Pf}。！？｡]","break":true}],"abbreviations":["N.B","div","a.C","fig","d.p.R","c.c.p","Cfr","vol","Geom","O.d.G","S.p.A","ver","N.d.A","dott","arch","d.C","N.d.T","rag","Sig","Mod","pag","dr","tav","N.d.E","DC","mitt","Ing","int","C.P","ag","L","U.S","S.M.A.R.T","p.i","tab","Ltd","Liv","D","U.S.A","sez","avv","S.A.R","p"],"number_abbreviations":["n","N","art","Art","cap","Cap","pag","vol","Vol"]}$def$),
    ('builtin:pt@1', 'Portuguese', 'pt', 'Portuguese abbreviations (Unicode CLDR, curated) and sentence-ending punctuation.',
     $def${"mode":"sentence","rules":[{"before":"(?:^|\\P{L})\\p{Ll}\\.","after":"","break":false},{"before":"[.!?…][\\p{Pe}\\p{Pf}\"']*","after":"[\\t\\n\\v\\f\\r\\x{85}\\p{Z}]+[\\p{Lu}\\p{Lt}\\p{Lo}\\p{Nd}\\p{Ps}\\p{Pi}\"'¿¡]","break":true},{"before":"[。！？｡]","after":"[^\\p{Pe}\\p{Pf}。！？｡]","break":true}],"abbreviations":["psicol","fig","compl","rep","cap","doc","fisiol","dipl","astron","port","eletrôn","geom","mov","ago","trad","arquit","dez","ed","apt","Exmo","col","ff","univ","res","R","transp","l","des","fev","abr","liter","lat","Dir","cf","adm","fot","p.m","P.M","créd","jur","anat","dir","end","fís","E","Est","cont","matem","Drs","gên","neol","pág","índ","Ltda","Exma","esp","ingl","tecnol","Mar","símb","Pe","pal","filos","V.T","fasc","vs","mai","S.A","profa","N.Sra","r.s.v.p","cel","mat","abrev","out","long","aux","arit","aer","jul","lin","S","méd","odontol","org","A.C","jun","déb","Av","álg","sup","fl","odont","caps","relat","organiz","hist","Fr","Ilmo","fem","ap","Ltd","pol","séc","prof","cx","nov","quím","mús","agric","W.C","fr","cat","jan","pron","rel","autom","Sta","Dra","p","tel","div","p. ex","a.C","bras","Alm","Dr","comp","pq","arqueol","náut","biogr","f","círc","fac","d.C","apart","ex","Jr","set","tec","sociol","gram","ind","Ilma","vol","eng","rod","Ph.D","Dras","pp","elem","máq","cód","eletr","prod","ref","fil","a.m","obs","N.T","contab","Sto","lit","educ","desc","próx"],"number_abbreviations":["n","N","art","Art","cap","Cap","pág","vol","Vol"]}$def$),
    ('builtin:ru@1', 'Russian', 'ru', 'Russian abbreviations (Unicode CLDR, curated) and sentence-ending punctuation.',
     $def${"mode":"sentence","rules":[{"before":"(?:^|\\P{L})\\p{Ll}\\.","after":"","break":false},{"before":"[.!?…][\\p{Pe}\\p{Pf}\"']*","after":"[\\t\\n\\v\\f\\r\\x{85}\\p{Z}]+[\\p{Lu}\\p{Lt}\\p{Lo}\\p{Nd}\\p{Ps}\\p{Pi}\"'¿¡]","break":true},{"before":"[。！？｡]","after":"[^\\p{Pe}\\p{Pf}。！？｡]","break":true}],"abbreviations":["руб","янв","до н. э","сент","тел","дек","февр","нояб","апр","н. э","окт","тыс","авг","проф","н.э","кв","ул","отд"],"number_abbreviations":["с","стр","рис","гл","т","ст","п"]}$def$),
    ('builtin:th@1', 'Thai', 'th', 'Thai has no sentence-ending punctuation, so each paragraph is one segment.',
     $def${"mode":"paragraph","rules":[],"abbreviations":[],"number_abbreviations":[]}$def$);

-- The rule set a file was imported with, in full (segmentation.Snapshot), so
-- every later parse of the file splits it the same way. NULL: the file was
-- stored before rule sets existed and uses builtin:conservative@1.
ALTER TABLE files ADD COLUMN segmentation jsonb;

-- +goose Down
ALTER TABLE files DROP COLUMN segmentation;
DROP VIEW segmentation_rule_set_catalog;
DROP TABLE segmentation_rule_sets;
DROP TABLE segmentation_builtin_rule_sets;
DROP SEQUENCE segmentation_rule_set_id_seq;
