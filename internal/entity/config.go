package entity

import pkgentity "github.com/yogasw/wick/pkg/entity"

// Config is re-exported from pkg/entity so existing internal callers
// keep working. Module authors should import pkg/entity directly.
type Config = pkgentity.Config

// StructToConfigs is re-exported from pkg/entity — see that package
// for the tag grammar and reflection rules.
var StructToConfigs = pkgentity.StructToConfigs
