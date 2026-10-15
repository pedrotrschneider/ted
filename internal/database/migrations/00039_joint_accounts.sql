-- +goose Up
CREATE TABLE joint_account_members (
 joint_id bigint NOT NULL REFERENCES users(id),
 member_id bigint NOT NULL REFERENCES users(id),
 color text NOT NULL CHECK(color ~ '^#[0-9A-Fa-f]{6}$'),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(joint_id,member_id),
 CHECK(joint_id<>member_id)
);
CREATE INDEX joint_account_member ON joint_account_members(member_id,joint_id);
-- +goose StatementBegin
CREATE FUNCTION check_joint_membership() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(84051002);
 IF EXISTS(SELECT 1 FROM joint_account_members WHERE member_id=NEW.joint_id OR joint_id=NEW.member_id) THEN
  RAISE EXCEPTION 'nested joint membership is not allowed' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER joint_membership_flat BEFORE INSERT OR UPDATE OF joint_id,member_id ON joint_account_members FOR EACH ROW EXECUTE FUNCTION check_joint_membership();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'preserve joint membership; restore a complete backup to roll back'; END $$;
-- +goose StatementEnd
