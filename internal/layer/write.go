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
	batch            strings.Builder
	batchInserts     map[string]EntityInsert
	deleteBatch      strings.Builder
	batchSize        int
	flushThreshold   int
	appendMode       bool
	propertyMappings []*common.EntityToItemPropertyMapping
}

type EntityInsert struct {
	Id           string
	Recorded     uint64
	RowItem      *RowItem
	InsertString string
}

func (o *MysqlWriter) Write(entity *egdm.Entity) common.LayerError {
	item := &RowItem{Map: map[string]any{}}
	err := o.mapper.MapEntityToItem(entity, item)
	if err != nil {
		return common.Err(err, common.LayerErrorInternal)
	}
	// set the deleted flag, we always need this to do the right thing in upsert mode
	item.deleted = entity.IsDeleted

	// add delete statement to delete batch
	if o.deleteBatch.Len() == 0 {
		o.deleteBatch.WriteString("DELETE FROM ")
		o.deleteBatch.WriteString(o.table)
		o.deleteBatch.WriteString(" WHERE ")
	} else {
		o.deleteBatch.WriteString(" OR ")
	}
	o.deleteBatch.WriteString(o.idColumn)
	o.deleteBatch.WriteString(" = ")
	o.deleteBatch.WriteString(o.sqlVal(item.Map[o.idColumn], "id"))
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
		o.batch.Reset()
		o.deleteBatch.Reset()
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

func (o *MysqlWriter) sqlVal(v any, colName string) string {
	switch v.(type) {
	case string:
		for i, _ := range o.propertyMappings {
			if o.propertyMappings[i].Property == colName {
				if o.propertyMappings[i].Datatype == "datetime" {
					t, err := time.Parse(time.RFC3339, v.(string))
					if err != nil {
						return "NULL" // or handle the error as needed
					}
					v = t.Format("2006-01-02 15:04:05")
					return fmt.Sprintf("'%s'", v)
				} else if o.propertyMappings[i].Datatype == "timestamp" {
					t, err := time.Parse(time.RFC3339, v.(string))
					if err != nil {
						return "NULL" // or handle the error as needed
					}
					v = t.Format("2006-01-02 15:04:05-0700")
					return fmt.Sprintf("'%s'", v)
				}
			}
		}
		return fmt.Sprintf("'%s'", v)
	case nil:
		return "NULL"
	case bool:
		return fmt.Sprintf("'%t'", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func (o *MysqlWriter) flush() error {
	if o.batchSize == 0 {
		return nil
	}
	// execute the delete first
	delstmt := o.deleteBatch.String()
	if delstmt != "" {
		deltxn := "BEGIN;\n\n" + delstmt + ";\nCOMMIT;"
		o.logger.Debug(deltxn)
		_, err := o.tx.ExecContext(o.ctx, deltxn)
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
	var insertStatement strings.Builder
	for _, insert := range o.batchInserts {
		if len(insertStatement.String()) > 0 {
			insertStatement.WriteString(";\n")
		}
		insertStatement.WriteString(insert.InsertString)
	}

	stmt := "BEGIN;\n\n" + insertStatement.String() + ";\nCOMMIT;"
	o.logger.Debug(stmt)

	_, err := o.tx.ExecContext(o.ctx, stmt)
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

	for i, val := range item.Values {
		colName := item.Columns[i]
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(o.sqlVal(val, colName))
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
	batchInsert.InsertString = sb.String()
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
