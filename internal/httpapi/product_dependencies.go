package httpapi

import (
	"context"
	"fmt"

	runtimepolicy "cloudmeter/internal/runtime"
	"github.com/jackc/pgx/v5"
)

type dependencyQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const productDependencyGraphLock int64 = 729104503

func lockProductDependencyGraph(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", productDependencyGraphLock)
	return err
}

func validateProductDependencies(ctx context.Context, tx pgx.Tx, productID string, runtimeSpec map[string]any) error {
	dependencies, err := runtimepolicy.RuntimeDependencies(runtimeSpec)
	if err != nil {
		return err
	}
	for _, dependency := range dependencies {
		if dependency.ProductID == productID {
			return fmt.Errorf("dependency %q cannot target the same product", dependency.Key)
		}
		var available bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM app_products p JOIN app_product_versions pv ON pv.product_id=p.id
			WHERE p.id=$1 AND p.status='published' AND pv.published_at IS NOT NULL
		)`, dependency.ProductID).Scan(&available); err != nil {
			return err
		}
		if !available {
			return fmt.Errorf("dependency %q must target a published product", dependency.Key)
		}
		var cyclic bool
		if err = tx.QueryRow(ctx, `WITH RECURSIVE edges AS (
			SELECT pv.product_id AS source_id,(item->>'productId')::uuid AS target_id
			FROM app_product_versions pv CROSS JOIN LATERAL jsonb_array_elements(coalesce(pv.runtime_spec->'dependencies','[]'::jsonb)) item
			WHERE pv.published_at IS NOT NULL
			  AND item->>'productId' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
		), reachable(id) AS (
			SELECT $1::uuid UNION SELECT edges.target_id FROM reachable JOIN edges ON edges.source_id=reachable.id
		) SELECT EXISTS(SELECT 1 FROM reachable WHERE id=$2::uuid)`, dependency.ProductID, productID).Scan(&cyclic); err != nil {
			return err
		}
		if cyclic {
			return fmt.Errorf("dependency %q would create a product dependency cycle", dependency.Key)
		}
	}
	return nil
}

func missingRuntimeDependencies(ctx context.Context, query dependencyQuerier, userID, appID string, runtimeSpec map[string]any) ([]runtimepolicy.Dependency, error) {
	dependencies, err := runtimepolicy.RuntimeDependencies(runtimeSpec)
	if err != nil {
		return nil, err
	}
	missing := make([]runtimepolicy.Dependency, 0)
	for _, dependency := range dependencies {
		if !dependency.Required {
			continue
		}
		var ready bool
		if err = query.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM user_apps app JOIN app_routes route ON route.user_app_id=app.id
			WHERE app.user_id=$1 AND app.product_id=$2 AND app.service_slug=$3
			  AND app.status='running' AND app.last_successful_release_id IS NOT NULL
			  AND route.release_id=app.last_successful_release_id
			  AND ($4::text='' OR app.id::text<>$4::text)
		)`, userID, dependency.ProductID, dependency.ServiceSlug, appID).Scan(&ready); err != nil {
			return nil, err
		}
		if !ready {
			missing = append(missing, dependency)
		}
	}
	return missing, nil
}

func dependencyLabels(dependencies []runtimepolicy.Dependency) []string {
	labels := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		labels = append(labels, dependency.Key+" ("+dependency.ServiceSlug+")")
	}
	return labels
}
