package accounts

import (
	"context"
	"entertainmentdistrict/internal/database"
	"regexp"
)

type JointPerson struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	DisplayName   string `json:"display_name"`
	Color         string `json:"color,omitempty"`
	SharedStremio bool   `json:"shared_stremio"`
}
type JointSettings struct {
	Members     []JointPerson `json:"members"`
	Memberships []JointPerson `json:"memberships"`
	Candidates  []JointPerson `json:"candidates"`
}

const sharedStremioSQL = `EXISTS(SELECT 1 FROM stremio_links x JOIN stremio_links y ON y.account_id=x.account_id WHERE x.user_id=$1 AND y.user_id=u.id AND x.retired_at IS NULL AND y.retired_at IS NULL)`

func (s *Service) JointSettings(ctx context.Context, who Identity) (JointSettings, error) {
	out := JointSettings{Members: []JointPerson{}, Memberships: []JointPerson{}, Candidates: []JointPerson{}}
	if _, err := s.Current(ctx, who); err != nil {
		return out, err
	}
	rows, err := s.pool.Query(ctx, `SELECT 'member',u.id,u.username,u.display_name,j.color,`+sharedStremioSQL+` FROM joint_account_members j JOIN users u ON u.id=j.member_id WHERE j.joint_id=$1
 UNION ALL SELECT 'joint',u.id,u.username,u.display_name,j.color,`+sharedStremioSQL+` FROM joint_account_members j JOIN users u ON u.id=j.joint_id WHERE j.member_id=$1
 UNION ALL SELECT 'candidate',u.id,u.username,u.display_name,'',`+sharedStremioSQL+` FROM users u WHERE u.id<>$1 AND NOT u.disabled
 AND NOT EXISTS(SELECT 1 FROM joint_account_members WHERE member_id=$1 OR joint_id=u.id OR (joint_id=$1 AND member_id=u.id)) ORDER BY 4,2`, who.ID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var p JointPerson
		if err = rows.Scan(&kind, &p.ID, &p.Username, &p.DisplayName, &p.Color, &p.SharedStremio); err != nil {
			return out, err
		}
		switch kind {
		case "member":
			out.Members = append(out.Members, p)
		case "joint":
			out.Memberships = append(out.Memberships, p)
		default:
			out.Candidates = append(out.Candidates, p)
		}
	}
	return out, rows.Err()
}

var jointColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
var jointPalette = []string{"#59C6F2", "#F4BE52", "#E788BC", "#81CD94", "#B69BED", "#F59469"}

// Ownership comes only from the authenticated actor; callers cannot select an
// arbitrary joint account to edit. Colors belong to the viewing member.
func (s *Service) ChangeJoint(ctx context.Context, who Identity, action string, target int64, color string) error {
	if target <= 0 || target == who.ID {
		return ErrInvalid
	}
	if action != "add" && action != "remove" && action != "leave" && action != "color" {
		return ErrInvalid
	}
	if action == "color" && !jointColor.MatchString(color) {
		return ErrInvalid
	}
	tx, err := s.beginMutation(ctx)
	if err != nil {
		return err
	}
	defer database.Rollback(tx)
	if _, err = authorize(ctx, tx, who, false); err != nil {
		return err
	}
	switch action {
	case "add":
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$2 AND NOT disabled) AND NOT EXISTS(SELECT 1 FROM joint_account_members WHERE member_id=$1 OR joint_id=$2 OR (joint_id=$1 AND member_id=$2))`, who.ID, target).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrConflict
		}
		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM joint_account_members WHERE member_id=$1`, target).Scan(&n); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO joint_account_members(joint_id,member_id,color) VALUES($1,$2,$3)`, who.ID, target, jointPalette[n%len(jointPalette)])
	case "remove":
		var n int64
		tag, e := tx.Exec(ctx, `DELETE FROM joint_account_members WHERE joint_id=$1 AND member_id=$2`, who.ID, target)
		err = e
		n = tag.RowsAffected()
		if err == nil && n != 1 {
			return ErrNotFound
		}
	case "leave":
		tag, e := tx.Exec(ctx, `DELETE FROM joint_account_members WHERE joint_id=$2 AND member_id=$1`, who.ID, target)
		err = e
		if err == nil && tag.RowsAffected() != 1 {
			return ErrNotFound
		}
	case "color":
		tag, e := tx.Exec(ctx, `UPDATE joint_account_members SET color=$3 WHERE joint_id=$2 AND member_id=$1`, who.ID, target, color)
		err = e
		if err == nil && tag.RowsAffected() != 1 {
			return ErrNotFound
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
