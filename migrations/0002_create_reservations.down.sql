DROP TABLE IF EXISTS reservations;

-- btree_gist não é removido de propósito: extensões são baratas de manter e
-- caras de derrubar (outro objeto pode passar a depender dela). O down reverte
-- o schema desta migration, não o estado global do banco.
