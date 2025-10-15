package layer

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	common "github.com/mimiro-io/common-datalayer"
	egdm "github.com/mimiro-io/entity-graph-data-model"
)

func (d *Dataset) FullSync(ctx context.Context, batchInfo common.BatchInfo) (common.DatasetWriter, common.LayerError) {
	// TODO not supported (yet?)
	return nil, ErrNotSupported
}

func (d *Dataset) Incremental(ctx context.Context) (common.DatasetWriter, common.LayerError) {
	writer, err := d.newMysqlWriter(ctx)
	if err != nil {
		return nil, err
	}

	berr := writer.begin()
	return writer, common.Err(berr, common.LayerErrorInternal)
}

func (d *Dataset) newMysqlWriter(ctx context.Context) (*MysqlWriter, common.LayerError) {
	mapper := common.NewMapper(d.logger, d.datasetDefinition.IncomingMappingConfig, d.datasetDefinition.OutgoingMappingConfig)
	db := d.db.db
	tableName, ok := d.datasetDefinition.SourceConfig[TableName].(string)
	if !ok {
		return nil, ErrGeneric("table name not found in source config for dataset %s", d.datasetDefinition.DatasetName)
	}
	flushThreshold := 1000
	flushThresholdOverride, ok := d.datasetDefinition.SourceConfig[FlushThreshold]
	if ok {
		flushThresholdF, ok := flushThresholdOverride.(float64)
		if !ok {
			return nil, ErrGeneric("flush threshold must be an integer")
		}
		flushThreshold = int(flushThresholdF)
	}
	idColumn := "id"
	for _, m := range d.datasetDefinition.IncomingMappingConfig.PropertyMappings {
		if m.IsIdentity {
			idColumn = m.Property
			break
		}
	}
	propertyMappings := d.datasetDefinition.IncomingMappingConfig.PropertyMappings
	sinceColumn, _ := d.datasetDefinition.SourceConfig[SinceColumn].(string)
	sincePrecision, _ := d.datasetDefinition.SourceConfig[SincePrecision].(string)

	return &MysqlWriter{
		logger:           d.logger,
		mapper:           mapper,
		sinceColumn:      sinceColumn,
		sincePrecision:   sincePrecision,
		db:               db,
		ctx:              ctx,
		table:            tableName,
		flushThreshold:   flushThreshold,
		propertyMappings: propertyMappings,
		appendMode:       d.datasetDefinition.SourceConfig[AppendMode] == true,
		idColumn:         idColumn,
		batchInserts:     make(map[string]EntityInsert),
	}, nil
}

type MysqlWriter struct {
	logger           common.Logger
	ctx              context.Context
	mapper           *common.Mapper
	db               *sql.DB
	tx               *sql.Tx
	table            string
	idColumn         string
	sinceColumn      string
	sincePrecision   string
	batchInserts     map[string]EntityInsert
	deleteIds        []string
	batchSize        int
	flushThreshold   int
	appendMode       bool
	propertyMappings []*common.EntityToItemPropertyMapping
}

type EntityInsert struct {
	Id       string
	Recorded uint64
	RowItem  *RowItem
	Query    string
	Args     []any
}

func (o *MysqlWriter) Write(entity *egdm.Entity) common.LayerError {
	item := &RowItem{Map: map[string]any{}}
	err := o.mapper.MapEntityToItem(entity, item)
	if err != nil {
		return common.Err(err, common.LayerErrorInternal)
	}
	// set the deleted flag, we always need this to do the right thing in upsert mode
	item.deleted = entity.IsDeleted

	// add id to list of ids to delete (even the ones that will be inserted after)
	found := false
	for _, id := range o.deleteIds {
		if id == item.Map[o.idColumn].(string) {
			found = true
			break
		}
	}
	if !found {
		o.deleteIds = append(o.deleteIds, item.Map[o.idColumn].(string))
	}

	// if the entity is deleted continue
	if entity.IsDeleted {
		o.batchSize++
	} else {
		doInsert := false
		existing, exists := o.batchInserts[item.Map[o.idColumn].(string)]
		if exists {
			// already in batch, check which one is newer
			if entity.Recorded >= existing.Recorded {
				// replace existing with newer version
				o.batchInserts[item.Map[o.idColumn].(string)] = EntityInsert{
					Id:       item.Map[o.idColumn].(string),
					Recorded: entity.Recorded,
					RowItem:  item,
				}
				doInsert = true
			}
		} else {
			o.batchInserts[item.Map[o.idColumn].(string)] = EntityInsert{
				Id:       item.Map[o.idColumn].(string),
				Recorded: entity.Recorded,
				RowItem:  item,
			}
			doInsert = true
		}
		if doInsert {
			err = o.insert(o.batchInserts[item.Map[o.idColumn].(string)].RowItem)
			if err != nil {
				return common.Err(err, common.LayerErrorInternal)
			}
		}
	}

	if o.batchSize >= o.flushThreshold {
		err = o.flush()
		if err != nil {
			return common.Err(err, common.LayerErrorInternal)
		}
		o.batchSize = 0
		o.batchInserts = make(map[string]EntityInsert)
		o.deleteIds = []string{}
	}
	return nil
}

func (o *MysqlWriter) Close() common.LayerError {
	err := o.flush()
	if err != nil {
		return common.Err(err, common.LayerErrorInternal)
	}
	if o.tx != nil {
		err = o.tx.Commit()
		if err != nil {
			return common.Err(err, common.LayerErrorInternal)
		}
		o.logger.Debug("Transaction committed")
	}

	return nil
}

func (o *MysqlWriter) sqlArg(v any, colName string) (any, error) {
	switch typed := v.(type) {
	case string:
		for i := range o.propertyMappings {
			if o.propertyMappings[i].Property == colName {
				if o.propertyMappings[i].Datatype == "datetime" {
					t, err := time.Parse(time.RFC3339, typed)
					if err != nil {
						return nil, err
					}
					return t.Format("2006-01-02 15:04:05"), nil
				} else if o.propertyMappings[i].Datatype == "timestamp" {
					t, err := time.Parse(time.RFC3339, typed)
					if err != nil {
						return nil, err
					}
					return t.Format("2006-01-02 15:04:05-0700"), nil
				}
			}
		}
		return typed, nil
	case nil:
		return nil, nil
	case bool:
		return fmt.Sprintf("%t", typed), nil
	default:
		return typed, nil
	}
}

func (o *MysqlWriter) flush() error {
	if o.batchSize == 0 {
		return nil
	}
	// execute the delete first
	if len(o.deleteIds) > 0 {
		placeholders := make([]string, 0, len(o.deleteIds))
		args := make([]any, 0, len(o.deleteIds))
		for _, id := range o.deleteIds {
			arg, err := o.sqlArg(id, "id")
			if err != nil {
				return err
			}
			placeholders = append(placeholders, "?")
			args = append(args, arg)
		}

		deleteStatement := fmt.Sprintf(
			"DELETE FROM %s WHERE %s IN (%s)",
			o.table,
			o.idColumn,
			strings.Join(placeholders, ", "),
		)

		o.logger.Debug(deleteStatement)
		_, err := o.tx.ExecContext(o.ctx, deleteStatement, args...)
		if err != nil {
			if o.tx != nil {
				err2 := o.tx.Rollback()
				if err2 != nil {
					o.logger.Error("Failed to rollback transaction")
					return fmt.Errorf("failed to rollback transaction: %w, underlying: %w", err2, err)
				}
				o.logger.Debug("Delete transaction rolled back")
			}
			return err
		}
	}

	if len(o.batchInserts) == 0 {
		return nil
	}
	for _, insert := range o.batchInserts {
		o.logger.Debug(insert.Query)
		_, err := o.tx.ExecContext(o.ctx, insert.Query, insert.Args...)
		if err != nil {
			if o.tx != nil {
				err2 := o.tx.Rollback()
				if err2 != nil {
					o.logger.Error("Failed to rollback transaction")
					return fmt.Errorf("failed to rollback transaction: %w, underlying: %w", err2, err)
				}
				o.logger.Debug("Transaction rolled back")
			}
			return err
		}
	}

	return nil
}
func (o *MysqlWriter) insert(item *RowItem) error {
	// Always create a new INSERT statement for each item, but batch them together
	var sb strings.Builder
	sb.WriteString("INSERT INTO ")
	sb.WriteString(o.table)
	sb.WriteString(" (")

	for i, col := range item.Columns {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(strings.ToLower(col))
	}

	if o.sinceColumn != "" {
		sb.WriteString(", ")
		sb.WriteString(strings.ToLower(o.sinceColumn))
	}

	sb.WriteString(") VALUES (")

	args := make([]any, 0, len(item.Values))
	for i, val := range item.Values {
		colName := item.Columns[i]
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("?")
		arg, err := o.sqlArg(val, colName)
		if err != nil {
			return err
		}
		args = append(args, arg)
	}

	var sincePrecision string
	if o.sincePrecision != "" {
		sincePrecision = o.sincePrecision
	} else {
		sincePrecision = "6"
	}
	if o.sinceColumn != "" {
		sb.WriteString(", NOW(")
		sb.WriteString(sincePrecision)
		sb.WriteString(")")
	}

	sb.WriteString(")")

	batchInsert := o.batchInserts[item.Map[o.idColumn].(string)]
	batchInsert.Query = sb.String()
	batchInsert.Args = args
	o.batchInserts[item.Map[o.idColumn].(string)] = batchInsert

	o.batchSize++
	return nil
}

/*func (o *MysqlWriter) insert(item *RowItem) error {
	if o.batch.Len() == 0 {
		// Start building the INSERT statement
		o.batch.WriteString("INSERT INTO ")
		o.batch.WriteString(o.table)
		o.batch.WriteString(" (")
		for i, col := range item.Columns {
			if i > 0 {
				o.batch.WriteString(", ")
			}
			//o.batch.WriteString("\"")
			o.batch.WriteString(strings.ToLower(col))
			//o.batch.WriteString("\"")
		}

		if o.sinceColumn != "" {
			o.batch.WriteString(", ")
			o.batch.WriteString(strings.ToLower(o.sinceColumn))
			//o.batch.WriteString("\"")
		}

		o.batch.WriteString(") VALUES")
	} else {
		// Add a comma before next set of values
		o.batch.WriteString(",")
	}

	// Build a single row of values in parentheses
	o.batch.WriteString(" (")
	for i, val := range item.Values {
		colName := item.Columns[i]
		if i > 0 {
			o.batch.WriteString(", ")
		}
		o.batch.WriteString(o.sqlVal(val, colName))
	}
	var sincePrecision string
	if o.sincePrecision != "" {
		sincePrecision = o.sincePrecision
	} else {
		sincePrecision = "6"
	}
	if o.sinceColumn != "" {
		o.batch.WriteString((", NOW("))
		o.batch.WriteString(sincePrecision)
		o.batch.WriteString(")")
	}

	o.batch.WriteString(")")

	o.batchSize++
	return nil
}*/

func (o *MysqlWriter) begin() error {
	tx, err := o.db.Begin()
	if err != nil {
		return err
	}
	o.tx = tx
	o.logger.Debug("Transaction started")
	return nil
}
